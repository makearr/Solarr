package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

var (
	// ErrMigrationChanged reports a migration whose SQL differs from what was applied (§4.4).
	ErrMigrationChanged = errors.New("store: applied migration has been modified")

	// ErrPrefixCollision reports two owners claiming overlapping table prefixes.
	ErrPrefixCollision = errors.New("store: table prefix collides with another owner")

	// ErrPrefixViolation reports an owner creating a table outside its reserved
	// prefix, which is how one plugin ends up owning another's data.
	ErrPrefixViolation = errors.New("store: table is outside its owner's reserved prefix")

	// ErrBackupDirRequired reports migrations attempted with nowhere to back up to (Invariant 7,
	// ADR-15).
	ErrBackupDirRequired = errors.New("store: a backup directory is required before migrating")
)

// CoreOwner is the schema owner for MediaNexus itself. It reserves no prefix,
// because core tables are the unprefixed ones.
const CoreOwner = "core"

// Migration is one forward-only schema change (§4.4, ADR-15).
type Migration struct {
	Version int
	Up      string
	// DisablesForeignKeys asks applyMigration to run Up with foreign key enforcement
	// suspended (runWithForeignKeysDisabled), for a rebuild SQLite's own ALTER TABLE
	// cannot express any other way -- widening or narrowing a table's own constraint.
	// Set by migrations.load from a marker comment in the .sql file itself, so it is
	// locked into the migration's own checksum like everything else in the text.
	DisablesForeignKeys bool
}

// Owner is a component that owns part of the schema: core, or a plugin.
type Owner struct {
	// Name is "core", "media.tv", "feature.health".
	Name string
	// TablePrefix reserves a namespace: "tv_" belongs to media.tv. Empty means
	// unrestricted and is only valid for core.
	TablePrefix string
	Migrations  []Migration
}

const schemaMigrationDDL = `
CREATE TABLE IF NOT EXISTS schema_migration (
  owner       TEXT    NOT NULL,
  version     INTEGER NOT NULL,
  checksum    TEXT    NOT NULL,
  applied_at  INTEGER NOT NULL,
  PRIMARY KEY (owner, version)
) WITHOUT ROWID`

// Migrate applies every owner's pending migrations, in version order per owner (§4.4).
func Migrate(ctx context.Context, s *Store, backupDirectory string, owners ...Owner) error {
	if err := validateOwners(owners); err != nil {
		return err
	}
	if err := s.ensureMigrationTable(ctx); err != nil {
		return err
	}

	pending, err := s.pendingMigrations(ctx, owners)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	if backupDirectory == "" {
		return fmt.Errorf("%w: %d migrations pending on %s", ErrBackupDirRequired, len(pending), s.config.Name)
	}
	backupPath, err := s.Backup(ctx, backupDirectory, BackupPreMigration)
	if err != nil {
		return fmt.Errorf("pre-migration backup: %w", err)
	}
	s.logger.Info("backed up before migrating",
		slog.String("path", backupPath), slog.Int("pending", len(pending)))

	for _, step := range pending {
		if err := s.applyMigration(ctx, step); err != nil {
			return fmt.Errorf("migrate %s to version %d (restore %s to undo): %w",
				step.owner.Name, step.migration.Version, backupPath, err)
		}
	}
	return nil
}

type pendingMigration struct {
	owner     Owner
	migration Migration
}

// validateOwners enforces the registration-time rules: unique owner names, non-overlapping
// prefixes, and versions that are unique and ordered within an owner.
func validateOwners(owners []Owner) error {
	seenNames := make(map[string]bool, len(owners))
	for _, owner := range owners {
		if owner.Name == "" {
			return errors.New("store: migration owner has no name")
		}
		if seenNames[owner.Name] {
			return fmt.Errorf("store: duplicate migration owner %q", owner.Name)
		}
		seenNames[owner.Name] = true

		if owner.TablePrefix == "" && owner.Name != CoreOwner {
			return fmt.Errorf("store: owner %q must reserve a table prefix", owner.Name)
		}

		seenVersions := make(map[int]bool, len(owner.Migrations))
		for _, migration := range owner.Migrations {
			if migration.Version <= 0 {
				return fmt.Errorf("store: owner %q has migration version %d; versions start at 1",
					owner.Name, migration.Version)
			}
			if seenVersions[migration.Version] {
				return fmt.Errorf("store: owner %q has two migrations at version %d",
					owner.Name, migration.Version)
			}
			seenVersions[migration.Version] = true
		}
	}

	for i, first := range owners {
		for _, second := range owners[i+1:] {
			if first.TablePrefix == "" || second.TablePrefix == "" {
				continue
			}
			if strings.HasPrefix(first.TablePrefix, second.TablePrefix) ||
				strings.HasPrefix(second.TablePrefix, first.TablePrefix) {
				return fmt.Errorf("%w: %q reserves %q and %q reserves %q",
					ErrPrefixCollision, first.Name, first.TablePrefix, second.Name, second.TablePrefix)
			}
		}
	}
	return nil
}

func (s *Store) ensureMigrationTable(ctx context.Context) error {
	return s.Write(ctx, PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, schemaMigrationDDL)
		return err
	})
}

// pendingMigrations returns what has not been applied, verifying the checksum of
// everything that has.
func (s *Store) pendingMigrations(ctx context.Context, owners []Owner) ([]pendingMigration, error) {
	applied, err := s.appliedChecksums(ctx)
	if err != nil {
		return nil, err
	}

	var pending []pendingMigration
	for _, owner := range owners {
		sorted := append([]Migration(nil), owner.Migrations...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

		for _, migration := range sorted {
			key := migrationKey{owner: owner.Name, version: migration.Version}
			checksum := checksumOf(migration.Up)

			previous, wasApplied := applied[key]
			if !wasApplied {
				pending = append(pending, pendingMigration{owner: owner, migration: migration})
				continue
			}
			if previous != checksum {
				return nil, fmt.Errorf("%w: %s version %d was applied as %s but is now %s",
					ErrMigrationChanged, owner.Name, migration.Version, previous[:12], checksum[:12])
			}
		}
	}
	return pending, nil
}

type migrationKey struct {
	owner   string
	version int
}

func (s *Store) appliedChecksums(ctx context.Context) (map[migrationKey]string, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT owner, version, checksum FROM schema_migration")
	if err != nil {
		return nil, fmt.Errorf("store %s: read schema_migration: %w", s.config.Name, err)
	}
	defer func() { _ = rows.Close() }()

	applied := make(map[migrationKey]string)
	for rows.Next() {
		var key migrationKey
		var checksum string
		if err := rows.Scan(&key.owner, &key.version, &checksum); err != nil {
			return nil, fmt.Errorf("store %s: scan schema_migration: %w", s.config.Name, err)
		}
		applied[key] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: iterate schema_migration: %w", s.config.Name, err)
	}
	return applied, nil
}

// applyMigration runs one migration and records it in the same transaction, so
// a crash cannot leave the schema changed but unrecorded.
func (s *Store) applyMigration(ctx context.Context, step pendingMigration) error {
	before, err := s.tableNames(ctx)
	if err != nil {
		return err
	}

	runner := runInTransaction
	if step.migration.DisablesForeignKeys {
		runner = runWithForeignKeysDisabled
	}

	op := newWriteOp(ctx, PrioInteractive, s.clock.Now(), func(ctx context.Context, db *sql.DB) error {
		return runner(ctx, db, s.config.Name, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, step.migration.Up); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				"INSERT INTO schema_migration (owner, version, checksum, applied_at) VALUES (?, ?, ?, ?)",
				step.owner.Name, step.migration.Version, checksumOf(step.migration.Up), s.clock.Now().Unix())
			return err
		})
	})
	// A migration creating an index over an existing library is the one write
	// that is legitimately unbounded, and it runs before the API is serving.
	op.exemptFromBudget = true

	if err := s.queue.push(op); err != nil {
		return fmt.Errorf("store %s: %w", s.config.Name, err)
	}
	if err := <-op.result; err != nil {
		return err
	}

	return s.verifyPrefix(ctx, step.owner, before)
}

// verifyPrefix checks the tables a migration created against the owner's reserved namespace.
func (s *Store) verifyPrefix(ctx context.Context, owner Owner, before map[string]bool) error {
	if owner.TablePrefix == "" {
		return nil
	}
	after, err := s.tableNames(ctx)
	if err != nil {
		return err
	}
	for name := range after {
		if before[name] || strings.HasPrefix(name, owner.TablePrefix) {
			continue
		}
		return fmt.Errorf("%w: %q created %q, which is outside %q",
			ErrPrefixViolation, owner.Name, name, owner.TablePrefix)
	}
	return nil
}

func (s *Store) tableNames(ctx context.Context) (map[string]bool, error) {
	rows, err := s.read.QueryContext(ctx,
		"SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, fmt.Errorf("store %s: list tables: %w", s.config.Name, err)
	}
	defer func() { _ = rows.Close() }()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store %s: scan table name: %w", s.config.Name, err)
		}
		names[name] = true
	}
	return names, rows.Err()
}

func checksumOf(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}
