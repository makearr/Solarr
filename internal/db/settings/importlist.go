package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ImportList is one per-library *Arr-shaped import list (ADR-38, todo 4.16).
// Scaffolding only: nothing yet syncs against it (stage 12).
type ImportList struct {
	ID               int64
	CategoryID       int64
	Name             string
	Type             string
	URL              string
	CadenceHours     int64
	Monitor          bool
	QualityProfileID *int64
	Exclusions       string
	Enabled          bool
}

// ImportListRequest is what CreateImportList/UpdateImportList accept --
// naming-and-policy only, matching WorkIdentity's own "this struct decides
// nothing about persistence" shape.
type ImportListRequest struct {
	Name             string
	Type             string
	URL              string
	CadenceHours     int64
	Monitor          bool
	QualityProfileID *int64
	Exclusions       string
	Enabled          bool
}

func validateImportList(request ImportListRequest) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(request.Name) == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "not empty"})
	}
	if strings.TrimSpace(request.Type) == "" {
		problems = append(problems, config.Problem{Path: "type", Expectation: "not empty"})
	}
	if request.CadenceHours <= 0 {
		problems = append(problems, config.Problem{
			Path: "cadence_hours", Value: fmt.Sprint(request.CadenceHours), Expectation: "a positive number of hours",
		})
	}
	return problems
}

// ImportLists reads a category's own import lists. Deliberately bypasses the
// snapshot (unlike exclusion_pattern/quality_profile): nothing else in this
// process reads an import list yet -- the sync job that would (stage 12)
// does not exist -- so there is no reader to keep consistent, and a plain
// query is simpler than extending buildSnapshot for one.
func (c *Cache) ImportLists(ctx context.Context, categoryID int64) ([]ImportList, error) {
	rows, err := c.queries.ListImportListsForCategory(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("settings: list import lists for category %d: %w", categoryID, err)
	}
	lists := make([]ImportList, len(rows))
	for index, row := range rows {
		lists[index] = convertImportList(row)
	}
	return lists, nil
}

// CreateImportList adds one import list, refusing an unknown category the
// same way SetCategoryAccess does.
func (c *Cache) CreateImportList(ctx context.Context, categoryID int64, request ImportListRequest) (ImportList, error) {
	if _, found := c.Snapshot().Category(categoryID); !found {
		return ImportList{}, fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if problems := validateImportList(request); len(problems) > 0 {
		return ImportList{}, problems
	}

	var created settingsdb.ImportList
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		created, err = c.queries.WithTx(tx).CreateImportList(ctx, settingsdb.CreateImportListParams{
			CategoryID: categoryID, Name: request.Name, Type: request.Type, Url: request.URL,
			CadenceHours: request.CadenceHours, Monitor: boolToInt(request.Monitor),
			QualityProfileID: nullInt64(request.QualityProfileID), Exclusions: request.Exclusions,
			Enabled: boolToInt(request.Enabled),
		})
		return err
	})
	if err != nil {
		return ImportList{}, fmt.Errorf("settings: create import list: %w", err)
	}
	return convertImportList(created), nil
}

// UpdateImportList replaces one import list's fields wholesale, refusing a
// missing id.
func (c *Cache) UpdateImportList(ctx context.Context, id int64, request ImportListRequest) (ImportList, error) {
	if problems := validateImportList(request); len(problems) > 0 {
		return ImportList{}, problems
	}

	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := c.queries.WithTx(tx)
		if _, err := queries.GetImportList(ctx, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: import list %d", ErrNotFound, id)
			}
			return err
		}
		return queries.UpdateImportList(ctx, settingsdb.UpdateImportListParams{
			Name: request.Name, Type: request.Type, Url: request.URL,
			CadenceHours: request.CadenceHours, Monitor: boolToInt(request.Monitor),
			QualityProfileID: nullInt64(request.QualityProfileID), Exclusions: request.Exclusions,
			Enabled: boolToInt(request.Enabled), ID: id,
		})
	})
	if err != nil {
		return ImportList{}, fmt.Errorf("settings: update import list %d: %w", id, err)
	}

	updated, err := c.queries.GetImportList(ctx, id)
	if err != nil {
		return ImportList{}, fmt.Errorf("settings: read back import list %d: %w", id, err)
	}
	return convertImportList(updated), nil
}

// DeleteImportList removes one import list. Deleting one that does not
// exist is not an error -- DELETE already answers "gone" either way.
func (c *Cache) DeleteImportList(ctx context.Context, id int64) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return c.queries.WithTx(tx).DeleteImportList(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("settings: delete import list %d: %w", id, err)
	}
	return nil
}

func convertImportList(row settingsdb.ImportList) ImportList {
	return ImportList{
		ID: row.ID, CategoryID: row.CategoryID, Name: row.Name, Type: row.Type, URL: row.Url,
		CadenceHours: row.CadenceHours, Monitor: row.Monitor != 0,
		QualityProfileID: int64Ptr(row.QualityProfileID), Exclusions: row.Exclusions,
		Enabled: row.Enabled != 0,
	}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
