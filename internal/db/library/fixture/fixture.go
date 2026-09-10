// Package fixture builds a library.db at ADR-22 scale, so the ADR-28 targets and
// the ADR-5 driver comparison measure a realistic corpus rather than a microbenchmark.
package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/migrations"
)

// generatorVersion changes whenever the generated rows change shape, so a cached
// fixture from an older generator is rebuilt rather than answering new benchmarks.
const generatorVersion = 1

// DefaultFiles keeps an ordinary test run to seconds; ADR-22's 500k is opt-in.
const DefaultFiles = 20_000

// rowsPerTransaction is Invariant 2's bulk chunk: every phase stays inside it.
const rowsPerTransaction = 200

// Spec describes a corpus. Files is the ADR-22 figure the ADR-28 targets are stated at.
type Spec struct {
	Files int
	Seed  int64
}

func (s Spec) withDefaults() Spec {
	if s.Files <= 0 {
		s.Files = DefaultFiles
	}
	if s.Seed == 0 {
		s.Seed = 1
	}
	return s
}

// FileName identifies one corpus, so two specs never share a cached database. The schema
// is in the name as well as the generator: a migration changes the plans a benchmark
// measures, and a cached file from before it would answer for the new one without saying
// so.
func (s Spec) FileName() (string, error) {
	s = s.withDefaults()
	schema, err := schemaFingerprint()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("library-%dfiles-seed%d-g%d-s%s.db", s.Files, s.Seed, generatorVersion, schema), nil
}

// schemaFingerprint covers the migrations' contents, not their count. An edited
// migration at the same version is exactly the case a version number misses, and it is
// the case the migration runner itself refuses for the same reason.
func schemaFingerprint() (string, error) {
	owner, err := migrations.Library()
	if err != nil {
		return "", fmt.Errorf("fixture: %w", err)
	}

	digest := sha256.New()
	for _, migration := range owner.Migrations {
		_, _ = fmt.Fprintf(digest, "%d\n%s\n", migration.Version, migration.Up)
	}
	return hex.EncodeToString(digest.Sum(nil))[:12], nil
}

// Ensure returns the path of a generated database, building it only the first time.
func Ensure(ctx context.Context, filesystem fsx.FS, directory string, spec Spec) (string, error) {
	spec = spec.withDefaults()
	if !filepath.IsAbs(directory) {
		return "", fmt.Errorf("fixture: directory %q must be absolute", directory)
	}
	name, err := spec.FileName()
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, name)

	switch _, err := filesystem.Stat(path); {
	case err == nil:
		return path, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("fixture: check %s: %w", path, err)
	}

	if err := filesystem.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("fixture: create %s: %w", directory, err)
	}

	// Built under another name and renamed, so an interrupted build is never
	// picked up later as a complete corpus.
	building := path + ".building"
	if err := discard(filesystem, building); err != nil {
		return "", err
	}
	if err := Build(ctx, filesystem, building, spec); err != nil {
		return "", err
	}
	// The destination has no database, but it can still have sidecars from one that
	// was interrupted, and SQLite would replay that WAL into the new file.
	if err := discard(filesystem, path); err != nil {
		return "", err
	}
	if err := filesystem.Rename(building, path); err != nil {
		return "", fmt.Errorf("fixture: publish %s: %w", path, err)
	}
	return path, nil
}

// Build writes a fresh corpus at path, migrating the schema first.
func Build(ctx context.Context, filesystem fsx.FS, path string, spec Spec) error {
	spec = spec.withDefaults()

	opened, err := store.Open(ctx, clock.NewSystem(), store.Config{
		Name:   "library",
		Path:   path,
		FS:     filesystem,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	defer func() { _ = opened.Close() }()

	backups := filepath.Join(filepath.Dir(path), "backups")
	if err := filesystem.MkdirAll(backups, 0o755); err != nil {
		return fmt.Errorf("fixture: create %s: %w", backups, err)
	}
	owner, err := migrations.Library()
	if err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	if err := store.Migrate(ctx, opened, backups, owner); err != nil {
		return fmt.Errorf("fixture: %w", err)
	}
	return Populate(ctx, opened, spec)
}

// discard removes a database and the WAL sidecars that outlive it.
func discard(filesystem fsx.FS, path string) error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := filesystem.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("fixture: remove %s: %w", path+suffix, err)
		}
	}
	return nil
}
