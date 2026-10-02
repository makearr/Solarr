package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// userTagSource marks tag assignments a person made, which a provider refresh never clears (ADR-33).
const userTagSource = "user"

// ProfileOverride answers the quality profile an item was given of its own; found is false for the library's.
func (d Desired) ProfileOverride(ctx context.Context, itemID int64) (profileID int64, found bool, err error) {
	profileID, err = librarydb.New(d.store.Read()).GetDesiredProfileOverride(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: profile override of item %d: %w", itemID, err)
	}
	return profileID, true, nil
}

// SetProfileOverride gives an item its own quality profile; nil returns it to the library's.
func (d Desired) SetProfileOverride(ctx context.Context, itemID int64, profileID *int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if profileID == nil {
			return queries.ClearDesiredProfileOverride(ctx, itemID)
		}
		if err := queries.SetDesiredProfileOverride(ctx, librarydb.SetDesiredProfileOverrideParams{
			DesiredItemID: itemID, ProfileID: *profileID,
		}); err != nil {
			return err
		}
		return queries.MarkDesiredItemDirty(ctx, itemID)
	})
	if err != nil {
		return fmt.Errorf("library: set profile override of item %d: %w", itemID, err)
	}
	return nil
}

// TagName is one tag as the edit dialog offers it.
type TagName struct {
	ID   int64
	Name string
}

// ListTags answers every tag, by name.
func (p People) ListTags(ctx context.Context) ([]TagName, error) {
	rows, err := librarydb.New(p.store.Read()).ListTagNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list tags: %w", err)
	}
	tags := make([]TagName, len(rows))
	for i, row := range rows {
		tags[i] = TagName{ID: row.ID, Name: row.Name}
	}
	return tags, nil
}

// UserTags answers the tags a person assigned to a work.
func (p People) UserTags(ctx context.Context, workID int64) ([]TagName, error) {
	rows, err := librarydb.New(p.store.Read()).ListTagNamesForSubjectBySource(ctx,
		librarydb.ListTagNamesForSubjectBySourceParams{SubjectType: "work", SubjectID: workID, Source: userTagSource})
	if err != nil {
		return nil, fmt.Errorf("library: user tags of work %d: %w", workID, err)
	}
	tags := make([]TagName, len(rows))
	for i, row := range rows {
		tags[i] = TagName{ID: row.TagID, Name: row.TagName}
	}
	return tags, nil
}

// SetUserTags makes names the work's user-assigned tags, creating the ones that do not exist yet;
// provider-assigned tags are untouched.
func (p People) SetUserTags(ctx context.Context, workID int64, names []string) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.ClearProviderTagAssignments(ctx, librarydb.ClearProviderTagAssignmentsParams{
			SubjectType: "work", SubjectID: workID, Source: userTagSource,
		}); err != nil {
			return fmt.Errorf("clear existing: %w", err)
		}
		for _, name := range names {
			tagID, err := findOrCreateTag(ctx, queries, name, userTagSource)
			if err != nil {
				return fmt.Errorf("tag %q: %w", name, err)
			}
			if err := queries.AssignTag(ctx, librarydb.AssignTagParams{
				TagID: tagID, SubjectType: "work", SubjectID: workID, Source: userTagSource,
			}); err != nil {
				return fmt.Errorf("assign tag %q: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: set user tags of work %d: %w", workID, err)
	}
	return nil
}
