package main

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
)

// worksMonitorAdapter satisfies apiserver.WorksMonitor over library.Desired,
// resolving category/profile the same way music.SubscriptionExpandJob
// already does (CategoryFinder.CategoryForMediaType) rather than needing a
// category the caller has no natural way to supply -- a work-detail page has
// a work, not a root folder, to derive one from.
type worksMonitorAdapter struct {
	works            library.Works
	desired          library.Desired
	categories       music.CategoryFinder
	defaultProfileID int64
}

// SetWorkMonitored always ensures the album item exists, monitored or not:
// an explicit "not interested" from a user is worth recording the same way
// as a category's monitor_discovered policy would (Invariant 71 -- once it
// exists, a later scan or subscription pass leaves it alone either way).
func (a worksMonitorAdapter) SetWorkMonitored(
	ctx context.Context, workID int64, monitored bool,
) (apiserver.WantedRow, error) {
	detail, err := a.works.Detail(ctx, workID)
	if err != nil {
		return apiserver.WantedRow{}, err
	}

	category, ok := a.categories.CategoryForMediaType(detail.Work.MediaType)
	if !ok {
		return apiserver.WantedRow{}, fmt.Errorf("no category configured for %s", detail.Work.MediaType)
	}
	profileID := a.defaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}

	item, err := a.desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profileID, Monitored: monitored,
	})
	if err != nil {
		return apiserver.WantedRow{}, err
	}
	if (item.Monitored == 1) != monitored {
		if err := a.desired.SetItemMonitored(ctx, item.ID, monitored); err != nil {
			return apiserver.WantedRow{}, err
		}
		item.Monitored = boolToInt64(monitored)
	}

	row := apiserver.WantedRow{ID: item.ID, Monitored: monitored, PresentCount: item.PresentCount, State: item.State}
	if item.RequiredCount.Valid {
		row.RequiredCount = &item.RequiredCount.Int64
	}
	return row, nil
}

func (a worksMonitorAdapter) SetPartMonitored(ctx context.Context, workID, partID int64, monitored bool) error {
	return a.desired.SetPartMonitored(ctx, workID, partID, monitored)
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
