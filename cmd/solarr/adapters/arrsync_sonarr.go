package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/tv"
)

// TVAdder is the TV service's add, which a Sonarr import maps each series through.
type TVAdder interface {
	Add(ctx context.Context, request tv.AddRequest) (tv.AddResult, error)
}

// syncSonarr adds every series Sonarr holds by its TVDB id, best-effort per series. A series Solarr already holds is
// matched and left as it is; one it creates takes Sonarr's monitored flags, series and season. Series are counted
// in the Artists columns, the run's first-kind tally.
func (a ArrSync) syncSonarr(
	ctx context.Context, importID int64, instance settings.ArrInstance, client arrimport.SonarrClient, options apiserver.ArrSyncOptions,
) (library.ArrImportCounts, error) {
	if _, err := client.SystemStatus(ctx); err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("connect: %w", err)
	}
	all, err := client.Series(ctx)
	if err != nil {
		return library.ArrImportCounts{}, fmt.Errorf("list series: %w", err)
	}
	if a.TV == nil {
		return library.ArrImportCounts{}, errors.New("no TV service")
	}
	roots := a.Cache.Snapshot().RootFolders()

	var counts library.ArrImportCounts
	var failures []string
	wanted := func(monitored bool) bool {
		return (monitored && options.Monitored) || (!monitored && options.Unmonitored)
	}
	var total int64
	for _, series := range all {
		if wanted(series.Monitored) {
			total++
		}
	}
	var walked int64
	for _, series := range all {
		if !wanted(series.Monitored) {
			continue
		}
		a.Progress.set(importID, walked, total)
		walked++
		created, err := a.syncSeries(ctx, importID, instance, roots, series, options)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", series.Title, err))
			continue
		}
		if created {
			counts.ArtistsCreated++
		} else {
			counts.ArtistsMatched++
		}
	}
	if len(failures) > 0 {
		return counts, errors.New(strings.Join(failures, "; "))
	}
	return counts, nil
}

func (a ArrSync) syncSeries(
	ctx context.Context, importID int64, instance settings.ArrInstance, roots []settings.RootFolder,
	series arrimport.SonarrSeries, options apiserver.ArrSyncOptions,
) (bool, error) {
	if series.TVDBID == 0 {
		return false, errors.New("no TVDB id")
	}
	categoryID, ok := a.categoryFor(instance, rewritePath(series.RootFolderPath, options.PathRewrites), roots)
	if !ok {
		return false, fmt.Errorf("no category matches root folder %q", series.RootFolderPath)
	}
	category, _ := a.Cache.Snapshot().Category(categoryID)
	monitor := tv.MonitorNone
	if series.Monitored {
		monitor = tv.MonitorAll
	}
	result, err := a.TV.Add(ctx, tv.AddRequest{
		IDs:        tv.IDs{TVDB: series.TVDBID, TVmaze: series.TVMazeID, TMDB: series.TMDBID, IMDb: series.IMDbID},
		CategoryID: categoryID, ProfileID: a.Categories.ProfileFor(category, a.DefaultProfileID),
		Monitor: monitor, Specials: true,
	})
	if err != nil {
		return false, err
	}
	if result.Created && series.Monitored {
		for _, season := range series.Seasons {
			if season.Monitored {
				continue
			}
			if _, err := a.Desired.SetSeasonMonitored(ctx, result.WorkID, tv.SeasonRef(season.SeasonNumber), false); err != nil {
				return false, fmt.Errorf("season %d: %w", season.SeasonNumber, err)
			}
		}
	}
	workID := result.WorkID
	if _, err := a.Imports.AddItem(ctx, importID, library.NewArrImportItem{
		Kind: library.ArrImportItemSeries, SourceID: strconv.FormatInt(series.ID, 10), Title: series.Title,
		WorkID: &workID, WorkCreated: result.Created,
	}, a.Clock.Now().Unix()); err != nil {
		return false, err
	}
	return result.Created, nil
}
