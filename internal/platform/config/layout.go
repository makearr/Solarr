package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// legacyDataDirName is the folder the databases, cache and backups sat in before the 2026-11 layout.
const legacyDataDirName = "data"

// Move is one folder MigrateLegacyLayout relocated.
type Move struct{ From, To string }

// MigrateLegacyLayout moves db, cache and backups out of <config_dir>/data to <config_dir> when config.yml still names
// the old locations, and rewrites those lines in config.yml. A path set to anything else is left alone.
func MigrateLegacyLayout(fsys fsx.FS, configPath string, loaded Config) (Config, []Move, error) {
	var moves []Move
	targets := []struct {
		name    string
		setting string
		field   *string
	}{
		{"db", "paths.db_dir", &loaded.Paths.DBDir},
		{"cache", "paths.cache_dir", &loaded.Paths.CacheDir},
		{"backups", "paths.backup_dir", &loaded.Paths.BackupDir},
	}
	document, readErr := readDocument(fsys, configPath)
	for _, target := range targets {
		legacy := filepath.Join(loaded.Paths.ConfigDir, legacyDataDirName, target.name)
		if *target.field != legacy {
			continue
		}
		destination := filepath.Join(loaded.Paths.ConfigDir, target.name)
		moved, err := moveLegacyDirectory(fsys, legacy, destination)
		if err != nil {
			return loaded, moves, err
		}
		if moved {
			moves = append(moves, Move{From: legacy, To: destination})
		}
		*target.field = destination
		if readErr == nil {
			if patched, err := setString(document, target.setting, destination); err == nil {
				document = patched
			}
		}
	}
	if len(moves) > 0 && readErr == nil {
		if err := fsx.WriteAtomic(fsys, configPath, func(w io.Writer) error {
			_, err := w.Write(document)
			return err
		}); err != nil {
			return loaded, moves, fmt.Errorf("config: rewrite %s after moving the data folders: %w", configPath, err)
		}
	}
	// The old parent is empty once its three children moved; a failure here means something else lives in it.
	_ = fsys.Remove(filepath.Join(loaded.Paths.ConfigDir, legacyDataDirName))
	return loaded, moves, nil
}

func moveLegacyDirectory(fsys fsx.FS, legacy, destination string) (bool, error) {
	if _, err := fsys.Stat(legacy); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("config: inspect %s: %w", legacy, err)
	}
	if _, err := fsys.Stat(destination); err == nil {
		return false, fmt.Errorf("config: both %s and %s exist; merge them by hand, then restart", legacy, destination)
	}
	if err := fsys.Rename(legacy, destination); err != nil {
		return false, fmt.Errorf("config: move %s to %s: %w", legacy, destination, err)
	}
	return true, nil
}

func readDocument(fsys fsx.FS, path string) ([]byte, error) {
	file, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, MaxDocumentBytes))
}

// setString replaces one string at a dotted path, adding the key under its section when the file has none.
func setString(data []byte, dotted, value string) ([]byte, error) {
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	path, err := yaml.PathString("$." + dotted)
	if err != nil {
		return nil, fmt.Errorf("config: build path: %w", err)
	}
	if err := path.ReplaceWithReader(file, strings.NewReader(value)); err != nil {
		return nil, fmt.Errorf("config: set %s: %w", dotted, err)
	}
	return []byte(file.String()), nil
}
