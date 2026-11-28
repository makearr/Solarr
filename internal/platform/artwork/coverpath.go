package artwork

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// shardOf is the folder, of 256, a picture file name lives in: 260,000 files in one folder make every listing and lookup slow.
func shardOf(name string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return fmt.Sprintf("%02x", hash.Sum32()%256)
}

// CoverPath is where a library picture is written: its shard folder under dir.
func CoverPath(dir, name string) string { return filepath.Join(dir, shardOf(name), name) }

// ShardedFor makes the folder a picture is written into and answers its path.
func ShardedFor(fsys fsx.FS, dir, name string) (string, error) {
	if err := fsys.MkdirAll(filepath.Join(dir, shardOf(name)), 0o755); err != nil {
		return "", err
	}
	return CoverPath(dir, name), nil
}

// ReadPath is where to read a library picture from: its shard folder, or the folder itself for a file the move to
// shards has not reached yet.
func ReadPath(fsys fsx.FS, dir, name string) string {
	if sharded := CoverPath(dir, name); fileExists(fsys, sharded) {
		return sharded
	}
	if legacy := filepath.Join(dir, name); fileExists(fsys, legacy) {
		return legacy
	}
	return CoverPath(dir, name)
}

func fileExists(fsys fsx.FS, path string) bool {
	_, err := fsys.Stat(path)
	return err == nil
}

// moveChunk bounds the files one ShardCovers call moves.
const moveChunk = 1000

// ShardCovers moves the pictures lying directly in dir into their shard folders, a chunk at a time, and answers how
// many moved; call it until it answers 0.
func ShardCovers(fsys fsx.FS, dir string) (int, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	moved := 0
	for _, entry := range entries {
		if entry.IsDir || !IsPictureName(entry.Name) {
			continue
		}
		destination, err := ShardedFor(fsys, dir, entry.Name)
		if err != nil {
			return moved, err
		}
		if err := fsys.Rename(filepath.Join(dir, entry.Name), destination); err != nil {
			return moved, err
		}
		if moved++; moved >= moveChunk {
			break
		}
	}
	return moved, nil
}

// IsPictureName says a file name is a stored picture of any format.
func IsPictureName(name string) bool {
	_, ok := ParseFormat(strings.TrimPrefix(filepath.Ext(name), "."))
	return ok
}
