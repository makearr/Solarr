package adapters

import (
	"context"
	"fmt"
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
