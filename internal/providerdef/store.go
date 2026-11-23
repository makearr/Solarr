package providerdef

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

const (
	builtinDir = "builtin"
	customDir  = "custom"
	removedDir = ".removed"
	// readmeName is rewritten at every start.
	readmeName = "README.md"
)

// FileProblems is what one file of a folder is wrong with.
type FileProblems struct {
	File     string
	Problems []Problem
}

// Store reads and writes the definition folders under root (the config folder's providers/).
type Store struct {
	FS   fsx.FS
	Root string
}

// folder is a kind's folder, named in the plural where the kind is a thing a person lists (indexers, webhooks).
func (s Store) folder(kind Kind, which string) string {
	name := string(kind)
	if kind == KindIndexer || kind == KindWebhook {
		name += "s"
	}
	return path.Join(s.Root, name, which)
}

// Prepare creates every folder, writes the README and, for each kind, the built-in files: write-if-different, stale files removed,
// custom/ never entered. Built-in definitions are always served from the embedded data; this folder is only a readable mirror.
func (s Store) Prepare(ctx context.Context, readme []byte, builtin map[Kind]map[string][]byte) error {
	for _, kind := range Kinds {
		for _, which := range []string{builtinDir, customDir} {
			if err := s.FS.MkdirAll(s.folder(kind, which), 0o755); err != nil {
				return fmt.Errorf("providerdef: create %s: %w", s.folder(kind, which), err)
			}
		}
	}
	if err := s.writeIfDifferent(path.Join(s.Root, readmeName), readme); err != nil {
		return err
	}
	var failed []error
	for _, kind := range Kinds {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.mirror(kind, builtin[kind]); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

func (s Store) mirror(kind Kind, files map[string][]byte) error {
	dir := s.folder(kind, builtinDir)
	for name, data := range files {
		if err := s.writeIfDifferent(path.Join(dir, name+".yaml"), data); err != nil {
			return err
		}
	}
	entries, err := s.FS.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("providerdef: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		name, isYAML := strings.CutSuffix(entry.Name, ".yaml")
		if entry.IsDir || !isYAML {
			continue
		}
		if _, shipped := files[name]; !shipped {
			if err := s.FS.Remove(path.Join(dir, entry.Name)); err != nil {
				return fmt.Errorf("providerdef: remove %s: %w", entry.Name, err)
			}
		}
	}
	return nil
}

func (s Store) writeIfDifferent(file string, data []byte) error {
	if current, err := s.read(file); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return fsx.WriteAtomic(s.FS, file, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

func (s Store) read(file string) ([]byte, error) {
	opened, err := s.FS.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()
	return io.ReadAll(io.LimitReader(opened, MaxFileBytes+1))
}

// LoadCustom reads a kind's custom/*.yaml: at most MaxFilesPerKind, none above MaxFileBytes, sub-folders (.removed) skipped.
// A missing folder is no problem; a bad file is reported and the others still load.
func (s Store) LoadCustom(kind Kind) ([]Definition, []FileProblems, error) {
	dir := s.folder(kind, customDir)
	entries, err := s.FS.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("providerdef: read %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir && strings.HasSuffix(entry.Name, ".yaml") {
			names = append(names, entry.Name)
		}
	}
	slices.Sort(names)
	var defs []Definition
	var reports []FileProblems
	for index, name := range names {
		if index == MaxFilesPerKind {
			reports = append(reports, FileProblems{File: name, Problems: []Problem{{Severity: SeverityError, Code: CodeTooLarge, Field: "files"}}})
			continue
		}
		data, err := s.read(path.Join(dir, name))
		if err != nil {
			reports = append(reports, FileProblems{File: name, Problems: []Problem{{Severity: SeverityError, Code: CodeSyntax}}})
			continue
		}
		def, problems := Parse(kind, name, data, SourceCustom)
		if len(problems) > 0 {
			reports = append(reports, FileProblems{File: name, Problems: problems})
		}
		if !hasError(problems) {
			defs = append(defs, def)
		}
	}
	return defs, reports, nil
}

// MaxRemovedFiles is how many files custom/.removed/ keeps.
const MaxRemovedFiles = 50

func (s Store) customFile(kind Kind, key string) string {
	return path.Join(s.folder(kind, customDir), key+".yaml")
}

// ReadCustom reads a kind's custom file by key; fs.ErrNotExist when there is none.
func (s Store) ReadCustom(kind Kind, key string) ([]byte, error) {
	return s.read(s.customFile(kind, key))
}

// WriteCustom writes a custom file through a temporary file and a rename, so a reader never sees half of it.
func (s Store) WriteCustom(kind Kind, key string, data []byte) error {
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("providerdef: %q is not a definition key", key)
	}
	if err := s.FS.MkdirAll(s.folder(kind, customDir), 0o755); err != nil {
		return err
	}
	return fsx.WriteAtomic(s.FS, s.customFile(kind, key), func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// RemoveCustom moves a custom file to custom/.removed/<key>.<unix>.yaml and prunes that folder to MaxRemovedFiles; nothing is
// ever unlinked but the oldest removed copies.
func (s Store) RemoveCustom(kind Kind, key string, unix int64) error {
	removed := s.folder(kind, customDir) + "/" + removedDir
	if err := s.FS.MkdirAll(removed, 0o755); err != nil {
		return err
	}
	if err := s.FS.Rename(s.customFile(kind, key), path.Join(removed, fmt.Sprintf("%s.%d.yaml", key, unix))); err != nil {
		return err
	}
	if entries, err := s.FS.ReadDir(removed); err == nil {
		s.pruneRemoved(removed, entries)
	}
	return nil
}

// pruneRemoved unlinks the oldest removed copies above MaxRemovedFiles; a copy that cannot be unlinked stays, which is harmless.
func (s Store) pruneRemoved(removed string, entries []fsx.DirEntry) {
	var names []string
	for _, entry := range entries {
		if !entry.IsDir && strings.HasSuffix(entry.Name, ".yaml") {
			names = append(names, entry.Name)
		}
	}
	// The unix time ends every name, so the oldest sort by it, not by key.
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(stamp(a), stamp(b)) })
	for len(names) > MaxRemovedFiles {
		_ = s.FS.Remove(path.Join(removed, names[0]))
		names = names[1:]
	}
}

// stamp is the unix time of a removed file's name, zero-padded so it sorts as text.
func stamp(name string) string {
	trimmed := strings.TrimSuffix(name, ".yaml")
	digits := trimmed[strings.LastIndex(trimmed, ".")+1:]
	return strings.Repeat("0", max(0, 20-len(digits))) + digits
}
