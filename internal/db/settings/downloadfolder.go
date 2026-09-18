package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// DownloadFolder is one watched upstream-download destination (todo 4.18,
// section 5.1). Registry only -- nothing yet acts on what lands there,
// that is stage 5.
type DownloadFolder struct {
	ID         int64
	Path       string
	Label      string
	CategoryID *int64
	// Readable is probed once, at creation, by the caller (apiserver owns
	// the real filesystem check, the same split AddRootFolder already
	// keeps). Nothing refreshes it after -- the same gap root_folder.writable
	// had before the capacity job existed to poll it.
	Readable       bool
	ReadableReason string
}

// DownloadFolderRequest is what CreateDownloadFolder accepts.
type DownloadFolderRequest struct {
	Path           string
	Label          string
	CategoryID     *int64
	Readable       bool
	ReadableReason string
}

func validateDownloadFolder(request DownloadFolderRequest) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(request.Path) == "" {
		problems = append(problems, config.Problem{Path: "path", Expectation: "not empty"})
	}
	return problems
}

// DownloadFolders reads every configured download folder. Deliberately
// bypasses the snapshot, the same as ImportLists -- nothing else in this
// process reads one yet, so there is no reader to keep consistent.
func (c *Cache) DownloadFolders(ctx context.Context) ([]DownloadFolder, error) {
	rows, err := c.queries.ListDownloadFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list download folders: %w", err)
	}
	folders := make([]DownloadFolder, len(rows))
	for index, row := range rows {
		folders[index] = convertDownloadFolder(row)
	}
	return folders, nil
}

// GetDownloadFolder answers one download folder by id. A missing id surfaces
// sql.ErrNoRows, wrapped.
func (c *Cache) GetDownloadFolder(ctx context.Context, id int64) (DownloadFolder, error) {
	row, err := c.queries.GetDownloadFolder(ctx, id)
	if err != nil {
		return DownloadFolder{}, fmt.Errorf("settings: get download folder %d: %w", id, err)
	}
	return convertDownloadFolder(row), nil
}

// CreateDownloadFolder adds one download folder. A given category_id must
// already exist, the same check CreateImportList makes.
func (c *Cache) CreateDownloadFolder(ctx context.Context, request DownloadFolderRequest) (DownloadFolder, error) {
	if request.CategoryID != nil {
		if _, found := c.Snapshot().Category(*request.CategoryID); !found {
			return DownloadFolder{}, fmt.Errorf("%w: category %d", ErrNotFound, *request.CategoryID)
		}
	}
	if problems := validateDownloadFolder(request); len(problems) > 0 {
		return DownloadFolder{}, problems
	}

	var created settingsdb.DownloadFolder
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateDownloadFolder(ctx, settingsdb.CreateDownloadFolderParams{
			Path: request.Path, Label: sql.NullString{String: request.Label, Valid: request.Label != ""},
			CategoryID: nullInt64(request.CategoryID), Readable: boolToInt(request.Readable),
			ReadableReason: sql.NullString{String: request.ReadableReason, Valid: request.ReadableReason != ""},
		})
		return err
	})
	if err != nil {
		return DownloadFolder{}, fmt.Errorf("settings: create download folder: %w", err)
	}
	return convertDownloadFolder(created), nil
}

// DeleteDownloadFolder removes one download folder. Deleting one that does
// not exist is not an error -- DELETE already answers "gone" either way.
func (c *Cache) DeleteDownloadFolder(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteDownloadFolder(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete download folder %d: %w", id, err)
	}
	return nil
}

func convertDownloadFolder(row settingsdb.DownloadFolder) DownloadFolder {
	return DownloadFolder{
		ID: row.ID, Path: row.Path, Label: row.Label.String, CategoryID: int64Ptr(row.CategoryID),
		Readable: row.Readable != 0, ReadableReason: row.ReadableReason.String,
	}
}
