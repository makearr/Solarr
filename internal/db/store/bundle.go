package store

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Bundle names: solarr-backup.<stamp>.tar.zst holds <name>.db for each database and config.yml.
const (
	bundlePrefix = "solarr-backup."
	bundleSuffix = ".tar.zst"
	// ConfigMember is the bundled config.yml's name inside the archive.
	ConfigMember = "config.yml"
)

// ErrInvalidBundle means a backup bundle failed its check.
var ErrInvalidBundle = errors.New("store: backup bundle is not valid")

// BundleInfo is one backup bundle on disk.
type BundleInfo struct {
	Path string
	At   time.Time
}

// BundleMember is one file going into a bundle, named as it will be inside the archive.
type BundleMember struct{ Name, Path string }

// WriteBundle packs the members into a new bundle in directory, atomically, and answers its path.
func WriteBundle(fsys fsx.FS, directory string, at time.Time, members []BundleMember) (string, error) {
	stamp := strings.ReplaceAll(at.UTC().Format("2006-01-02T15:04:05Z"), ":", "-")
	destination := filepath.Join(directory, bundlePrefix+stamp+bundleSuffix)
	if _, err := fsys.Lstat(destination); err == nil {
		return "", fmt.Errorf("store: backup bundle %q already exists", destination)
	}
	err := fsx.WriteAtomicSecret(fsys, destination, func(w io.Writer) error {
		encoder, err := zstd.NewWriter(w)
		if err != nil {
			return err
		}
		archive := tar.NewWriter(encoder)
		for _, member := range members {
			if err := addMember(fsys, archive, member); err != nil {
				return fmt.Errorf("add %s: %w", member.Name, err)
			}
		}
		if err := archive.Close(); err != nil {
			return err
		}
		return encoder.Close()
	})
	if err != nil {
		return "", fmt.Errorf("store: write backup bundle %q: %w", destination, err)
	}
	return destination, nil
}

func addMember(fsys fsx.FS, archive *tar.Writer, member BundleMember) error {
	info, err := fsys.Stat(member.Path)
	if err != nil {
		return err
	}
	source, err := fsys.Open(member.Path)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	if err := archive.WriteHeader(&tar.Header{Name: member.Name, Mode: 0o600, Size: info.Size, ModTime: info.ModTime}); err != nil {
		return err
	}
	_, err = io.Copy(archive, source)
	return err
}

// ListBundles answers the bundles in directory, newest first. A missing directory has none.
func ListBundles(fsys fsx.FS, directory string) ([]BundleInfo, error) {
	entries, err := fsys.ReadDir(directory)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list backup bundles in %q: %w", directory, err)
	}
	var found []BundleInfo
	for _, entry := range entries {
		if entry.IsDir || !strings.HasPrefix(entry.Name, bundlePrefix) || !strings.HasSuffix(entry.Name, bundleSuffix) {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(entry.Name, bundlePrefix), bundleSuffix)
		if at, err := time.Parse(backupStampLayout, stamp); err == nil {
			found = append(found, BundleInfo{Path: filepath.Join(directory, entry.Name), At: at})
		}
	}
	slices.SortFunc(found, func(a, b BundleInfo) int { return strings.Compare(b.Path, a.Path) })
	return found, nil
}

// ExtractMember writes one member of a bundle to destination, atomically.
func ExtractMember(fsys fsx.FS, bundlePath, member, destination string) error {
	return readBundle(fsys, bundlePath, func(name string, content io.Reader) (bool, error) {
		if name != member {
			return false, nil
		}
		err := fsx.WriteAtomicSecret(fsys, destination, func(w io.Writer) error {
			_, err := io.Copy(w, content)
			return err
		})
		return true, err
	})
}

// readBundle walks the archive until visit answers true; a bundle without the member it looked for is an error.
func readBundle(fsys fsx.FS, bundlePath string, visit func(name string, content io.Reader) (bool, error)) error {
	file, err := fsys.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("store: open backup bundle %q: %w", bundlePath, err)
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return fmt.Errorf("store: read backup bundle %q: %w", bundlePath, err)
	}
	defer decoder.Close()
	archive := tar.NewReader(decoder)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("store: backup bundle %q: %w", bundlePath, fs.ErrNotExist)
		}
		if err != nil {
			return fmt.Errorf("store: read backup bundle %q: %w", bundlePath, err)
		}
		done, err := visit(path.Clean(header.Name), archive)
		if err != nil || done {
			return err
		}
	}
}

// BundleMembers lists the names a bundle holds.
func BundleMembers(fsys fsx.FS, bundlePath string) ([]string, error) {
	var names []string
	err := readBundle(fsys, bundlePath, func(name string, content io.Reader) (bool, error) {
		_, err := io.Copy(io.Discard, content)
		names = append(names, name)
		return false, err
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return names, err
}

// VerifyFile checks a database file on a connection of its own (the full integrity check).
func VerifyFile(ctx context.Context, fsys fsx.FS, name, databasePath string) error {
	return probe(ctx, Config{Name: name, Path: databasePath, FS: fsys}, true)
}

// VerifyBundle extracts every database of the bundle into scratch and checks each; scratch is left empty.
func VerifyBundle(ctx context.Context, fsys fsx.FS, bundlePath, scratch string) error {
	members, err := BundleMembers(fsys, bundlePath)
	if err != nil {
		return err
	}
	if !slices.Contains(members, ConfigMember) {
		return fmt.Errorf("store: backup bundle %q holds no %s", bundlePath, ConfigMember)
	}
	if err := fsys.MkdirAll(scratch, 0o700); err != nil {
		return err
	}
	checked := 0
	for _, member := range members {
		if !strings.HasSuffix(member, ".db") {
			continue
		}
		extracted := filepath.Join(scratch, member)
		if err := ExtractMember(fsys, bundlePath, member, extracted); err != nil {
			return err
		}
		err := VerifyFile(ctx, fsys, strings.TrimSuffix(member, ".db"), extracted)
		_ = fsys.Remove(extracted)
		if err != nil {
			return fmt.Errorf("store: backup bundle %q: %w", bundlePath, err)
		}
		checked++
	}
	if checked == 0 {
		return fmt.Errorf("store: backup bundle %q holds no database", bundlePath)
	}
	return nil
}

// PruneBundles removes bundles beyond keep (at least minBackupsKept) or, when cutoff is set, older than it; answers the removed paths.
func PruneBundles(fsys fsx.FS, directory string, keep int, cutoff time.Time) ([]string, error) {
	all, err := ListBundles(fsys, directory)
	if err != nil {
		return nil, err
	}
	var removed []string
	for index, bundle := range all {
		drop := index >= max(keep, minBackupsKept)
		if !cutoff.IsZero() {
			drop = index >= minBackupsKept && bundle.At.Before(cutoff)
		}
		if !drop {
			continue
		}
		if err := fsys.Remove(bundle.Path); err != nil {
			return removed, fmt.Errorf("store: remove backup bundle %q: %w", bundle.Path, err)
		}
		removed = append(removed, bundle.Path)
	}
	return removed, nil
}

// KeepOnlyLatestBundle checks the newest bundle is valid, then removes every older one; an invalid newest removes nothing.
func KeepOnlyLatestBundle(ctx context.Context, fsys fsx.FS, directory, scratch string) ([]string, error) {
	all, err := ListBundles(fsys, directory)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("store: no backup bundle in %q", directory)
	}
	if err := VerifyBundle(ctx, fsys, all[0].Path, scratch); err != nil {
		return nil, fmt.Errorf("%w, nothing was removed: %w", ErrInvalidBundle, err)
	}
	var removed []string
	for _, bundle := range all[1:] {
		if err := fsys.Remove(bundle.Path); err != nil {
			return removed, fmt.Errorf("store: remove backup bundle %q: %w", bundle.Path, err)
		}
		removed = append(removed, bundle.Path)
	}
	return removed, nil
}
