package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Link kinds a library_path records; LinkPrimary is the default and means "not linked to another path".
const (
	LinkPrimary  = "primary"
	LinkReflink  = "reflink"
	LinkHardlink = "hardlink"
	LinkSymlink  = "symlink"
)

// AddLinkedPath records a further path of a media object, placed as a link (or a copy, LinkPrimary) of one it has;
// a path already recorded for the object is fine, one recorded for another object is an error.
func (l ObjectLinks) AddLinkedPath(ctx context.Context, mediaObjectID, rootFolderID int64, relPath, kind string) error {
	return l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		existing, err := queries.GetPathByLocation(ctx, librarydb.GetPathByLocationParams{RootFolderID: rootFolderID, RelPath: relPath})
		if err == nil {
			if existing.MediaObjectID != mediaObjectID {
				return fmt.Errorf("library: %s is already another file", relPath)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("library: look up %s: %w", relPath, err)
		}
		now := l.store.Now().Unix()
		_, err = queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: relPath, LinkKind: kind, CreatedAt: now, LastSeenAt: now,
		})
		return err
	})
}

// ObjectLinks reads the media objects that live at several paths and records the paths turned into links.
type ObjectLinks struct{ store *store.Store }

// NewObjectLinks builds it over an open library store.
func NewObjectLinks(opened *store.Store) ObjectLinks { return ObjectLinks{store: opened} }

// SharedPath is one live path of a media object that lives at more than one place.
type SharedPath struct {
	PathID, MediaObjectID, RootFolderID int64
	RelPath, LinkKind                   string
	SizeBytes                           int64
}

// Shared lists every live path of every media object with two or more, primary first within an object.
func (l ObjectLinks) Shared(ctx context.Context) ([]SharedPath, error) {
	rows, err := librarydb.New(l.store.Read()).ListSharedObjectPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: shared object paths: %w", err)
	}
	paths := make([]SharedPath, len(rows))
	for i, row := range rows {
		paths[i] = SharedPath{
			PathID: row.ID, MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
			LinkKind: row.LinkKind, SizeBytes: row.SizeBytes,
		}
	}
	return paths, nil
}

// MarkLinked records how a path now shares its bytes with another.
func (l ObjectLinks) MarkLinked(ctx context.Context, pathID int64, kind string) error {
	return l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetLibraryPathLinkKind(ctx, librarydb.SetLibraryPathLinkKindParams{LinkKind: kind, ID: pathID})
	})
}
