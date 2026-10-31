package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// mergeChunk bounds the wanted items one write moves, so no transaction holds the library for long.
const mergeChunk = 200

// CategoryMerges moves what the library holds for one category to another, the data half of merging two libraries.
type CategoryMerges struct{ store *store.Store }

// NewCategoryMerges builds it over an open library store.
func NewCategoryMerges(opened *store.Store) CategoryMerges { return CategoryMerges{store: opened} }

// smallTables name a category by category_id and are few enough to move in one statement; a row the target already holds
// is left, then dropped.
var smallTables = []string{"request", "collection", "import_session", "move_job", "subscription"}

// Merge makes every wanted item, request, collection, import session, move and follow of category from category into's,
// and recomputes who and what each library holds. A wanted item the target already has for the same thing is dropped, not
// doubled. It answers how many wanted items moved.
func (m CategoryMerges) Merge(ctx context.Context, from, into int64) (int64, error) {
	if from == into {
		return 0, fmt.Errorf("library: a library cannot be merged into itself")
	}
	var moved, after int64
	for {
		ids, err := m.nextItems(ctx, from, after)
		if err != nil {
			return moved, err
		}
		if len(ids) == 0 {
			break
		}
		after = ids[len(ids)-1]
		if err := m.moveItems(ctx, ids, into); err != nil {
			return moved, err
		}
		moved += int64(len(ids))
	}
	err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		for _, table := range smallTables {
			if _, err := tx.ExecContext(ctx, "UPDATE OR IGNORE "+table+" SET category_id = ? WHERE category_id = ?", into, from); err != nil {
				return fmt.Errorf("move %s: %w", table, err)
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE category_id = ?", from); err != nil {
				return fmt.Errorf("drop %s: %w", table, err)
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM desired_item WHERE category_id = ?", from); err != nil {
			return fmt.Errorf("drop duplicate wanted items: %w", err)
		}
		for _, table := range []string{"person_library", "organisation_library"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE category_id = ?", from); err != nil {
				return fmt.Errorf("clear %s: %w", table, err)
			}
		}
		return nil
	})
	if err != nil {
		return moved, fmt.Errorf("library: merge library %d into %d: %w", from, into, err)
	}
	return moved, nil
}

func (m CategoryMerges) nextItems(ctx context.Context, from, after int64) ([]int64, error) {
	rows, err := m.store.Read().QueryContext(ctx, `SELECT id FROM desired_item WHERE category_id = ? AND id > ? ORDER BY id LIMIT ?`, from, after, mergeChunk)
	if err != nil {
		return nil, fmt.Errorf("library: wanted items of library %d: %w", from, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// moveItems re-files one chunk of wanted items and recomputes the people and organisations of their works.
func (m CategoryMerges) moveItems(ctx context.Context, ids []int64, into int64) error {
	return m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+1)
		args = append(args, into)
		for _, id := range ids {
			args = append(args, id)
		}
		// A work the target already wants in the same way is not wanted twice.
		if _, err := tx.ExecContext(ctx, `DELETE FROM desired_item WHERE id IN (`+marks+`) AND EXISTS (SELECT 1 FROM desired_item held
WHERE held.category_id = ? AND held.work_id = desired_item.work_id AND held.media_ref = desired_item.media_ref
  AND COALESCE(held.edition_id, 0) = COALESCE(desired_item.edition_id, 0))`, append(args[1:], into)...); err != nil {
			return fmt.Errorf("library: drop duplicate wanted items: %w", err)
		}
		// dirty: the planner works out what each now wants in its new library.
		if _, err := tx.ExecContext(ctx, "UPDATE OR IGNORE desired_item SET category_id = ?, dirty = 1 WHERE id IN ("+marks+")", args...); err != nil {
			return fmt.Errorf("library: move wanted items: %w", err)
		}
		workIDs, err := workIDsOf(ctx, tx, marks, args)
		if err != nil {
			return err
		}
		queries := librarydb.New(tx)
		for _, workID := range workIDs {
			if err := refreshWorkMembers(ctx, queries, workID); err != nil {
				return err
			}
		}
		return nil
	})
}

// workIDsOf are the works of the wanted items just moved; args are the target library and the item ids.
func workIDsOf(ctx context.Context, tx *sql.Tx, marks string, args []any) ([]int64, error) {
	works, err := tx.QueryContext(ctx, "SELECT DISTINCT work_id FROM desired_item WHERE category_id = ? AND id IN ("+marks+")", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = works.Close() }()
	var workIDs []int64
	for works.Next() {
		var id int64
		if err := works.Scan(&id); err != nil {
			return nil, err
		}
		workIDs = append(workIDs, id)
	}
	return workIDs, works.Err()
}
