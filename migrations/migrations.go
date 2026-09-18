// Package migrations carries the core schema as embedded SQL files, one per version per
// database (ADR-16).
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
)

//go:embed settings/*.sql jobs/*.sql library/*.sql metadata/*.sql
var files embed.FS

// disableForeignKeysMarker, present anywhere in a migration's own SQL, asks store.Migrate to run
// it with foreign key enforcement suspended (store.Migration.DisablesForeignKeys) -- needed for a
// rebuild ALTER TABLE cannot express (widening or narrowing a table's own constraint), since
// dropping a table other tables still hold live references into cascades those rows away unless
// enforcement is off for the duration. Part of the file's own text, so it is locked into the
// migration's checksum like everything else: changing it after the migration has applied is
// exactly the "changed migration" case store.Migrate already refuses.
const disableForeignKeysMarker = "-- migrate:disable-foreign-keys"

// Settings returns the migration set for settings.db.
func Settings() (store.Owner, error) { return owner("settings") }

// Jobs returns the migration set for jobs.db.
func Jobs() (store.Owner, error) { return owner("jobs") }

// Library returns the migration set for library.db.
func Library() (store.Owner, error) { return owner("library") }

// Metadata returns the migration set for cache/metadata.db (ADR-2). Disposable, but
// still schema-owned and versioned the same way as the three authoritative databases.
func Metadata() (store.Owner, error) { return owner("metadata") }

func owner(directory string) (store.Owner, error) {
	loaded, err := load(directory)
	if err != nil {
		return store.Owner{}, err
	}
	return store.Owner{Name: store.CoreOwner, Migrations: loaded}, nil
}

// load reads a directory of NNNN_name.sql files in version order.
func load(directory string) ([]store.Migration, error) {
	entries, err := fs.ReadDir(files, directory)
	if err != nil {
		return nil, fmt.Errorf("migrations: read %s: %w", directory, err)
	}

	migrations := make([]store.Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := versionOf(entry.Name())
		if err != nil {
			return nil, err
		}
		content, err := files.ReadFile(path.Join(directory, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrations: read %s: %w", entry.Name(), err)
		}
		migrations = append(migrations, store.Migration{
			Version: version, Up: string(content),
			DisablesForeignKeys: strings.Contains(string(content), disableForeignKeysMarker),
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

func versionOf(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found {
		return 0, fmt.Errorf("migrations: %q is not named NNNN_description.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migrations: %q does not start with a positive version number", name)
	}
	return version, nil
}
