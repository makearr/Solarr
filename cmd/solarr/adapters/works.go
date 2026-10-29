package adapters

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/priority"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// Works satisfies apiserver.WorksReader over library.Works. Clock resolves
// Fresh's cutoff date -- library.WorkFilter never calls time.Now() itself
// (forbidigo), so this is the one place that boundary is crossed.
type Works struct {
	Works library.Works
	Clock clock.Clock
	Fresh FreshWindow
	// Facets answers a media type's facets as the list reads them; nil lists no facets.
	Facets func(mediaType string) []library.WorkFacet
	// MediaTypeOf answers a library's media type.
	MediaTypeOf func(categoryID int64) (string, bool)
	// RatingLimit answers a library's maximum rating, false when it has none; nil filters nothing by rating.
	RatingLimit func(categoryID int64) (library.RatingLimit, bool)
}

// aboveRating is the list's Above rating filter for the one library it shows; a library without a limit holds
// nothing above it.
func aboveRating(on bool, categoryIDs []int64, limitOf func(int64) (library.RatingLimit, bool)) *library.RatingLimit {
	if !on {
		return nil
	}
	if len(categoryIDs) > 0 && limitOf != nil {
		if limit, found := limitOf(categoryIDs[0]); found {
			return &limit
		}
	}
	return &library.RatingLimit{MaxAge: math.MaxInt32, AllowUnrated: true}
}

// facetsFor are the facets a filter may name: the one library's type's, else every type's.
func (a Works) facetsFor(categoryIDs []int64) []library.WorkFacet {
	if a.Facets == nil {
		return nil
	}
	if len(categoryIDs) == 1 && a.MediaTypeOf != nil {
		if mediaType, ok := a.MediaTypeOf(categoryIDs[0]); ok {
			return a.Facets(mediaType)
		}
	}
	return a.Facets("")
}

func (a Works) ListWorks(
	ctx context.Context, filter apiserver.WorksFilter, cursor apiserver.WorksCursor, pageSize int,
) ([]apiserver.WorkRow, error) {
	var freshSince string
	var freshRSSSince int64
	if filter.Fresh {
		freshSince, freshRSSSince = a.Fresh.Cutoffs(a.Clock)
	}
	facets := a.facetsFor(filter.CategoryIDs)
	where, err := listfilter.Parse(filter.Where, library.FacetFields(library.WorkFields, facets))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	sort, sortFacet := library.WorkSort(filter.Sort), library.WorkFacet{}
	if key := filter.Sort.Facet(); key != "" {
		index := slices.IndexFunc(facets, func(facet library.WorkFacet) bool { return facet.Key == key && facet.Sortable })
		if index < 0 {
			return nil, fmt.Errorf("%w: %s cannot order this list", apiserver.ErrInvalidChoice, key)
		}
		sort, sortFacet = library.WorkSortFacet, facets[index]
	}
	rows, err := a.Works.List(ctx,
		library.WorkFilter{
			Where:           where,
			Facets:          facets,
			SortFacet:       sortFacet,
			RootFolderIDs:   filter.RootFolderIDs,
			CategoryIDs:     filter.CategoryIDs,
			Sort:            sort,
			Direction:       library.SortDirection(filter.Direction),
			FreshSince:      freshSince,
			FreshRSSSince:   freshRSSSince,
			WantedOnly:      filter.Wanted,
			CutoffUnmetOnly: filter.CutoffUnmet,
			TagIDs:          filter.TagIDs,
			ExcludedTagIDs:  filter.ExcludedTagIDs,
			AboveRating:     aboveRating(filter.AboveRating, filter.CategoryIDs, a.RatingLimit),
			MediaType:       filter.MediaType,
		},
		library.WorkCursor{
			SortTitle: cursor.SortTitle, SortYear: cursor.SortYear, SortAdded: cursor.SortAdded,
			SortArtist: cursor.SortArtist, SortSearched: cursor.SortSearched, SortImported: cursor.SortImported,
			SortNumber: cursor.SortNumber, SortText: cursor.SortText, ID: cursor.ID,
		},
		pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.WorkRow, len(rows))
	for index, row := range rows {
		view := apiserver.WorkRow{
			ID: row.ID, MediaType: row.MediaType, Title: row.CanonicalTitle, SortTitle: row.SortTitle,
			ArtistSortName: row.ArtistSortName, Narrators: row.Narrators,
			AddedAt: time.Unix(row.AddedAt, 0), UpdatedAt: time.Unix(row.UpdatedAt, 0),
		}
		if row.Year.Valid {
			view.Year = &row.Year.Int64
		}
		if row.Artist.Valid {
			view.Artist = &row.Artist.String
		}
		if row.WantedState.Valid {
			view.WantedState = &row.WantedState.String
		}
		if row.PresentCount.Valid {
			view.PresentCount = &row.PresentCount.Int64
		}
		if row.RequiredCount.Valid {
			view.RequiredCount = &row.RequiredCount.Int64
		}
		if row.AvailableFrom.Valid {
			at := time.Unix(row.AvailableFrom.Int64, 0).UTC()
			view.AvailableFrom = &at
		}
		view.Downloading = row.Downloading
		view.OnDisk = row.OnDisk
		view.Unverified = row.Unverified
		view.ArrivedAt = row.ArrivedAt
		view.SortNumber, view.SortText = row.SortNumber, row.SortText
		view.ArtistCover = row.ArtistCover
		view.NoArtwork = row.NoArtwork
		view.Monitored, view.Description, view.SizeBytes = row.Monitored, row.Description, row.SizeBytes
		if row.LastSearchedAt != 0 {
			at := time.Unix(row.LastSearchedAt, 0).UTC()
			view.LastSearchedAt = &at
		}
		view.PresentTotal, view.RequiredTotal, view.Upgradable = row.PresentTotal, row.RequiredTotal, row.Upgradable
		if row.LeadPersonID.Valid {
			view.LeadID = row.LeadPersonID.Int64
		}
		views[index] = view
	}
	return views, a.fillTypeFields(ctx, views)
}

// fillTypeFields reads each row's poster and its facets, by the facets of its own media type.
func (a Works) fillTypeFields(ctx context.Context, views []apiserver.WorkRow) error {
	byType := map[string][]int64{}
	ids := make([]int64, len(views))
	for i, view := range views {
		byType[view.MediaType] = append(byType[view.MediaType], view.ID)
		ids[i] = view.ID
	}
	posters, err := a.Works.Posters(ctx, ids)
	if err != nil {
		return err
	}
	for i := range views {
		views[i].PosterURL = posters[views[i].ID]
	}
	if a.Facets == nil {
		return nil
	}
	for mediaType, ids := range byType {
		values, err := a.Works.FacetValues(ctx, a.Facets(mediaType), ids)
		if err != nil {
			return err
		}
		badges, err := a.Works.Badges(ctx, a.Facets(mediaType), ids)
		if err != nil {
			return err
		}
		for i := range views {
			if views[i].MediaType == mediaType {
				badge := badges[views[i].ID]
				views[i].Facets, views[i].BadgeURL, views[i].BadgeName = values[views[i].ID], badge.Logo, badge.Name
			}
		}
	}
	return nil
}

// FreshRSSCutoff is ADR-55's availability window as a Unix time: what an RSS feed first showed since then is fresh.
func FreshRSSCutoff(cl clock.Clock, hours int64) int64 {
	return cl.Now().Add(-time.Duration(hours) * time.Hour).Unix()
}

// FreshWindow answers the configured freshness windows: days after release, and hours after an RSS sighting.
type FreshWindow func() (days, rssHours int64)

// values is the configured window, or ADR-55's defaults without one.
func (f FreshWindow) values() (days, rssHours int64) {
	if f == nil {
		return priority.DefaultNewReleaseDays, priority.RSSFreshHours
	}
	return f()
}

// Cutoffs resolves both windows against the clock: the release-date cutoff and the RSS one.
func (f FreshWindow) Cutoffs(cl clock.Clock) (since string, rssSince int64) {
	days, hours := f.values()
	return FreshSinceCutoff(cl, days), FreshRSSCutoff(cl, hours)
}

// FreshSinceCutoff resolves ADR-55's freshness window to a cutoff date
// string -- the one place library.WorkFilter.FreshSince's day-precision
// format boundary is crossed from a raw clock, shared by the Library list's
// own fresh=true filter (ListWorks above) and the dashboard's fresh-works
// counter sweep (cmd/solarr/app's freshCountsJob), so the two can never
// disagree about what "fresh" means.
func FreshSinceCutoff(cl clock.Clock, days int64) string {
	return cl.Now().AddDate(0, 0, -int(days)).Format(time.DateOnly)
}
