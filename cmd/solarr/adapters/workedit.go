package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// WorkEdit satisfies apiserver.WorkEditor: an album's monitoring, own quality profile and tags in one place.
type WorkEdit struct {
	Monitor  WorksMonitor
	Desired  library.Desired
	People   library.People
	Settings *settings.Cache
}

// GetWorkEdit answers what the dialog opens with; an album nothing ever wanted reads as unmonitored.
func (a WorkEdit) GetWorkEdit(ctx context.Context, workID int64) (apiserver.WorkEditRow, error) {
	if _, err := a.Monitor.Works.Detail(ctx, workID); err != nil {
		return apiserver.WorkEditRow{}, err
	}
	row := apiserver.WorkEditRow{Tags: []string{}}
	items, err := a.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	if len(items) > 0 {
		row.Monitored = items[0].Monitored == 1
		profileID, found, err := a.Desired.ProfileOverride(ctx, items[0].ID)
		if err != nil {
			return apiserver.WorkEditRow{}, err
		}
		if found {
			row.ProfileID = &profileID
		}
	}
	tags, err := a.People.UserTags(ctx, workID)
	if err != nil {
		return apiserver.WorkEditRow{}, err
	}
	for _, tag := range tags {
		row.Tags = append(row.Tags, tag.Name)
	}
	return row, nil
}

// SaveWorkEdit applies the whole dialog; a profile that does not exist is refused before anything is written.
func (a WorkEdit) SaveWorkEdit(ctx context.Context, workID int64, edit apiserver.WorkEditRow) error {
	if edit.ProfileID != nil {
		if _, found := a.Settings.Snapshot().QualityProfile(*edit.ProfileID); !found {
			return fmt.Errorf("quality profile %d: %w", *edit.ProfileID, apiserver.ErrInvalidWorkEdit)
		}
	}
	names := make([]string, 0, len(edit.Tags))
	for _, name := range edit.Tags {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	wanted, err := a.Monitor.SetWorkMonitored(ctx, workID, edit.Monitored)
	if err != nil {
		return err
	}
	if err := a.Desired.SetProfileOverride(ctx, wanted.ID, edit.ProfileID); err != nil {
		return err
	}
	return a.People.SetUserTags(ctx, workID, names)
}

// ListTags answers every tag name.
func (a WorkEdit) ListTags(ctx context.Context) ([]string, error) {
	tags, err := a.People.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(tags))
	for i, tag := range tags {
		names[i] = tag.Name
	}
	return names, nil
}

// BulkEdit applies change to every matching work through the same per-album edit the dialog uses, so one album failing
// (a work with no category, a since-deleted one) is logged and skipped rather than aborting the selection.
func (a WorkEdit) BulkEdit(
	ctx context.Context, selection apiserver.BulkSelection, change apiserver.BulkEditChange, preview bool,
) (int64, error) {
	if change.SetProfile && change.ProfileID != nil {
		if _, found := a.Settings.Snapshot().QualityProfile(*change.ProfileID); !found {
			return 0, fmt.Errorf("quality profile %d: %w", *change.ProfileID, apiserver.ErrInvalidWorkEdit)
		}
	}
	matched, err := a.Monitor.matchingWorkIDs(ctx, selection)
	if err != nil || preview {
		return int64(len(matched)), err
	}
	var affected int64
	for _, workID := range matched {
		edit, err := a.GetWorkEdit(ctx, workID)
		if err == nil {
			err = a.SaveWorkEdit(ctx, workID, applyBulkChange(edit, change))
		}
		if err != nil {
			a.Monitor.Logger.Warn("bulk edit: one work failed, continuing", slog.Int64("work_id", workID), slog.Any("error", err))
			continue
		}
		affected++
	}
	return affected, nil
}

// applyBulkChange is edit with change laid over it; tags compare case-insensitively, as the tag table's own slugs do.
func applyBulkChange(edit apiserver.WorkEditRow, change apiserver.BulkEditChange) apiserver.WorkEditRow {
	if change.Monitored != nil {
		edit.Monitored = *change.Monitored
	}
	if change.SetProfile {
		edit.ProfileID = change.ProfileID
	}
	removed := make(map[string]bool, len(change.RemoveTags))
	for _, name := range change.RemoveTags {
		removed[strings.ToLower(strings.TrimSpace(name))] = true
	}
	tags := make([]string, 0, len(edit.Tags)+len(change.AddTags))
	seen := map[string]bool{}
	for _, name := range append(slices.Clone(edit.Tags), change.AddTags...) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || removed[key] && !slices.Contains(change.AddTags, name) || seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, strings.TrimSpace(name))
	}
	edit.Tags = tags
	return edit
}
