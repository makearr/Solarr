package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ratingPageSize bounds one write over certifications or items (CLAUDE.md: bulk work in 200-row chunks).
const ratingPageSize = 200

// BackfillCertificationAges reads the age of each certification written before ages were stored, with the media
// type's own reading (ageOf; -1 when it cannot), a page at a time; it answers how many it read.
func (p People) BackfillCertificationAges(ctx context.Context, ageOf func(mediaType, rating string) int64) (int, error) {
	read := 0
	for {
		rows, err := librarydb.New(p.store.Read()).ListCertificationsWithoutAge(ctx, ratingPageSize)
		if err != nil {
			return read, fmt.Errorf("library: certifications without an age: %w", err)
		}
		if len(rows) == 0 {
			return read, nil
		}
		err = p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, row := range rows {
				if err := queries.SetCertificationAge(ctx, librarydb.SetCertificationAgeParams{
					Age: sql.NullInt64{Int64: ageOf(row.MediaType, row.Rating), Valid: true}, WorkID: row.WorkID, System: row.System,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return read, fmt.Errorf("library: certification ages: %w", err)
		}
		read += len(rows)
	}
}

// MarkLibraryDirty has the planner look at every item of a library again, after its rating limit changed.
func (d Desired) MarkLibraryDirty(ctx context.Context, categoryID int64) error {
	for {
		var changed int64
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			changed, err = librarydb.New(tx).MarkCategoryItemsDirtyBatch(ctx, librarydb.MarkCategoryItemsDirtyBatchParams{
				CategoryID: categoryID, BatchSize: ratingPageSize,
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("library: plan library %d again: %w", categoryID, err)
		}
		if changed < ratingPageSize {
			return nil
		}
	}
}

// ItemRating answers the library an item is in and the youngest age its work's certification admits, -1 when unrated.
func (d Desired) ItemRating(ctx context.Context, itemID int64) (categoryID, age int64, err error) {
	row, err := librarydb.New(d.store.Read()).GetItemRating(ctx, itemID)
	if err != nil {
		return 0, 0, fmt.Errorf("library: rating of item %d: %w", itemID, err)
	}
	return row.CategoryID, row.RatingAge, nil
}

// RatingLimit is a library's maximum rating: the oldest youngest-viewer age it admits, and whether it admits unrated.
type RatingLimit struct {
	MaxAge       int64
	AllowUnrated bool
}

// workRatingAge is the youngest age a work's certification admits, -1 when none is understood.
const workRatingAge = "CAST(COALESCE((SELECT MAX(wc.age) FROM work_certification wc WHERE wc.work_id = work.id AND wc.age >= 0), -1) AS INTEGER)"

// aboveRatingSQL holds a work a limit does not admit; its arguments are allow-unrated (0 or 1) and the age.
const aboveRatingSQL = "((" + workRatingAge + " < 0 AND ? = 0) OR " + workRatingAge + " > ?)"

// ViewerAge is an account's age limit: a work older than MaxAge is held from it, and so is an unrated one unless a
// library of the kids kind (KidsCategoryIDs) holds it.
type ViewerAge struct {
	MaxAge          int64
	KidsCategoryIDs []int64
}

// heldFromViewerSQL is true for a work the limit keeps from the account; its arguments are in the order returned.
func (v ViewerAge) heldFromViewerSQL() (string, []any) {
	over := workRatingAge + " > ?"
	if len(v.KidsCategoryIDs) == 0 {
		return "(" + over + " OR " + workRatingAge + " < 0)", []any{v.MaxAge}
	}
	fragment, arguments, _ := membershipFragment(
		"EXISTS (SELECT 1 FROM desired_item ki WHERE ki.work_id = work.id AND ki.category_id IN (%s))", v.KidsCategoryIDs)
	return "(" + over + " OR (" + workRatingAge + " < 0 AND NOT " + fragment + "))", append([]any{v.MaxAge}, arguments...)
}

// WorkHeldFromViewer says the limit keeps the work from the account.
func (p People) WorkHeldFromViewer(ctx context.Context, workID int64, limit ViewerAge) (bool, error) {
	held, arguments := limit.heldFromViewerSQL()
	var kept bool
	err := p.store.Read().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM work WHERE id = ? AND "+held+")",
		append([]any{workID}, arguments...)...).Scan(&kept)
	if err != nil {
		return false, fmt.Errorf("library: rating of work %d for an account: %w", workID, err)
	}
	return kept, nil
}

// CountAboveRating answers how many of a library's works its maximum rating does not admit; a live count on purpose, as the
// limit is whatever the preview asks about and no counter holds it.
func (w Works) CountAboveRating(ctx context.Context, categoryID int64, limit RatingLimit) (int64, error) {
	var count int64
	err := w.store.Read().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM work WHERE EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = work.id AND d.category_id = ?) AND "+aboveRatingSQL,
		categoryID, boolToInt(limit.AllowUnrated), limit.MaxAge).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("library: works above the rating of library %d: %w", categoryID, err)
	}
	return count, nil
}
