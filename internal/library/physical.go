package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// quickHashSampleBytes is how much of a file's head and tail the quick hash reads --
// section 10.4's "stat verify" layer, cheap enough to run on every newly seen path,
// unlike the exact oshash a background job computes later (ADR-35).
const quickHashSampleBytes = 65536

// QuickHash is media_object.quick_hash: size plus a head and tail sample. It is not a
// content hash -- two files differing only in the middle would collide -- which is the
// point: it is cheap identity, not proof, and a hash collision only costs a wasted
// dedupe lookup rather than a wrong one (size is checked too).
func QuickHash(fsys fsx.FS, absolutePath string, size int64) (string, error) {
	file, err := fsys.Open(absolutePath)
	if err != nil {
		return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	hash.Write(fmt.Appendf(nil, "%d:", size))

	head := make([]byte, min(int64(quickHashSampleBytes), size))
	if _, err := io.ReadFull(file, head); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
	}
	hash.Write(head)

	if size > quickHashSampleBytes {
		tail := make([]byte, quickHashSampleBytes)
		if _, err := file.ReadAt(tail, size-quickHashSampleBytes); err != nil {
			return "", fmt.Errorf("library: quick hash %s: %w", absolutePath, err)
		}
		hash.Write(tail)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// PhysicalFile is one file a scan found, with its quick hash already computed --
// Register never reads bytes itself, since a file read cannot happen while holding
// the write transaction it runs in (Invariant 2).
type PhysicalFile struct {
	RelPath     string
	SizeBytes   int64
	QuickHash   string
	FirstSeenAt time.Time
}

// Physical records what a scan found: media_object and library_path (section 5.2).
// Deciding what a file *is* belongs to a media type's Identifier chain (ADR-24); this
// only records that the bytes exist.
type Physical struct{ store *store.Store }

// NewPhysical builds it over an open library store.
func NewPhysical(opened *store.Store) Physical { return Physical{store: opened} }

// RegisterBatch finds or creates the media_object and library_path row for each file,
// in one transaction (Invariant 2) -- the same bound a scan batch already commits at.
// Results come back in the same order as files.
func (p Physical) RegisterBatch(ctx context.Context, rootFolderID int64, files []PhysicalFile) ([]int64, error) {
	ids := make([]int64, len(files))
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for index, file := range files {
			id, err := registerOne(ctx, queries, rootFolderID, file)
			if err != nil {
				return fmt.Errorf("library: register %s: %w", file.RelPath, err)
			}
			ids[index] = id
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func registerOne(
	ctx context.Context, queries *librarydb.Queries, rootFolderID int64, file PhysicalFile,
) (int64, error) {
	existing, err := queries.GetPathByLocation(ctx, librarydb.GetPathByLocationParams{
		RootFolderID: rootFolderID, RelPath: file.RelPath,
	})
	if err == nil {
		return existing.MediaObjectID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up: %w", err)
	}

	mediaObjectID, err := findOrCreateObject(ctx, queries, file)
	if err != nil {
		return 0, err
	}

	if _, err := queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
		MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: file.RelPath,
		LinkKind: "primary", IsPrimary: 1, CreatedAt: file.FirstSeenAt.Unix(),
	}); err != nil {
		return 0, fmt.Errorf("add path: %w", err)
	}
	return mediaObjectID, nil
}

// findOrCreateObject reuses an existing media_object sharing this quick hash and size
// -- a second path to bytes already known, such as a hardlink -- rather than creating
// a duplicate (ADR-11: "cross-entry: hardlink, else the user picks").
func findOrCreateObject(ctx context.Context, queries *librarydb.Queries, file PhysicalFile) (int64, error) {
	matches, err := queries.FindMediaObjectsByQuickHash(ctx, file.QuickHash)
	if err != nil {
		return 0, fmt.Errorf("find by quick hash: %w", err)
	}
	for _, match := range matches {
		if match.SizeBytes == file.SizeBytes {
			return match.ID, nil
		}
	}

	created, err := queries.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
		SizeBytes: file.SizeBytes, QuickHash: file.QuickHash,
		FirstSeenAt: file.FirstSeenAt.Unix(),
	})
	if err != nil {
		return 0, fmt.Errorf("create media object: %w", err)
	}
	return created, nil
}
