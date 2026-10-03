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
	SetEpisodeMonitoring(ctx context.Context, categoryID, workID int64, wanted map[tv.Key]bool) error
	SetProfile(ctx context.Context, categoryID, workID int64, profileID *int64) error
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
	profileNames := sonarrProfileNames(ctx, client)

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
		created, err := a.syncSeries(ctx, importID, instance, roots, series, options, client, profileNames)
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

// sonarrProfileNames reads the instance's quality profile names; a failure leaves the map empty, so every series
// takes its library's profile.
func sonarrProfileNames(ctx context.Context, client arrimport.SonarrClient) map[int64]string {
	profiles, err := client.QualityProfiles(ctx)
	if err != nil {
		return nil
	}
	names := make(map[int64]string, len(profiles))
	for _, profile := range profiles {
		names[profile.ID] = profile.Name
	}
	return names
}

func (a ArrSync) syncSeries(
	ctx context.Context, importID int64, instance settings.ArrInstance, roots []settings.RootFolder,
	series arrimport.SonarrSeries, options apiserver.ArrSyncOptions, client arrimport.SonarrClient, profileNames map[int64]string,
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
	if result.Created {
		if err := a.carryOverMonitoring(ctx, categoryID, result.WorkID, series, client); err != nil {
			return false, err
		}
		a.carryOverProfile(ctx, categoryID, result.WorkID, series, profileNames)
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

// sonarrEpisodes is the one call carryOverMonitoring makes of the Sonarr client.
type sonarrEpisodes interface {
	Episodes(ctx context.Context, seriesID int64) ([]arrimport.SonarrEpisode, error)
}

// carryOverMonitoring gives a series Solarr just created the monitoring Sonarr holds, episode by episode; when the
// episode list cannot be read, the season flags stand in.
func (a ArrSync) carryOverMonitoring(ctx context.Context, categoryID, workID int64, series arrimport.SonarrSeries, client sonarrEpisodes) error {
	if episodes, err := client.Episodes(ctx, series.ID); err == nil && len(episodes) > 0 {
		wanted := make(map[tv.Key]bool, len(episodes))
		for _, episode := range episodes {
			wanted[tv.Key{Season: episode.SeasonNumber, Number: episode.EpisodeNumber}] = series.Monitored && episode.Monitored
		}
		return a.TV.SetEpisodeMonitoring(ctx, categoryID, workID, wanted)
	}
	if !series.Monitored {
		return nil
	}
	for _, season := range series.Seasons {
		if season.Monitored {
			continue
		}
		if _, err := a.Desired.SetSeasonMonitored(ctx, workID, tv.SeasonRef(season.SeasonNumber), false); err != nil {
			return fmt.Errorf("season %d: %w", season.SeasonNumber, err)
		}
	}
	return nil
}

// carryOverProfile gives a created series the Solarr TV profile that has its Sonarr profile's name, when one does.
func (a ArrSync) carryOverProfile(ctx context.Context, categoryID, workID int64, series arrimport.SonarrSeries, profileNames map[int64]string) {
	name := profileNames[series.QualityProfileID]
	if name == "" {
		return
	}
	for _, profile := range a.Cache.Snapshot().QualityProfiles() {
		if profile.MediaType == tv.MediaType && strings.EqualFold(profile.Name, name) {
			id := profile.ID
			if err := a.TV.SetProfile(ctx, categoryID, workID, &id); err != nil && a.Logger != nil {
				a.Logger.Warn("sonarr import: quality profile not carried over", "series", series.Title, "error", err)
			}
			return
		}
	}
}
