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

	"github.com/makearr/MediaNexus/internal/store"
)

//go:embed settings/*.sql jobs/*.sql library/*.sql
var files embed.FS

// Settings returns the migration set for settings.db.
func Settings() (store.Owner, error) { return owner("settings") }

// Jobs returns the migration set for jobs.db.
func Jobs() (store.Owner, error) { return owner("jobs") }

// Library returns the migration set for library.db.
func Library() (store.Owner, error) { return owner("library") }

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
		migrations = append(migrations, store.Migration{Version: version, Up: string(content)})
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
