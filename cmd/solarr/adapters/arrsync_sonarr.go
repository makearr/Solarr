package adapters

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media/tv"
)

// TVAdder is the TV service's add, which a Sonarr import maps each series through.
type TVAdder interface {
	Add(ctx context.Context, request tv.AddRequest) (tv.AddResult, error)
	SetEpisodeMonitoring(ctx context.Context, categoryID, workID int64, wanted map[tv.Key]bool) error
	SetProfile(ctx context.Context, categoryID, workID int64, profileID *int64) error
	SetSeriesType(ctx context.Context, workID int64, kind tv.SeriesType) error
}

// sonarrSource walks Sonarr's series, four at once: each is added by its TVDB id. A series Solarr already holds is
// matched and left as it is; one it creates takes Sonarr's monitored flags, series and season. Series are counted in
// the Artists columns, the run's first-kind tally.
func (a ArrSync) sonarrSource(importID int64, client arrimport.SonarrClient) arrSource[arrimport.SonarrSeries] {
	var profileNames map[int64]string
	return arrSource[arrimport.SonarrSeries]{
		connect: func(ctx context.Context) error {
			if a.TV == nil {
				return errors.New("no TV service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		list: func(ctx context.Context) ([]arrimport.SonarrSeries, error) {
			profileNames = sonarrProfileNames(ctx, client)
			series, err := client.Series(ctx)
			if err == nil && a.WarmSeries != nil {
				ids := make([]string, 0, len(series))
				for _, one := range series {
					if one.TVDBID != 0 {
						ids = append(ids, strconv.FormatInt(one.TVDBID, 10))
					}
				}
				a.warm(ctx, "series", a.WarmSeries, ids)
			}
			return series, err
		},
		describe: func(series arrimport.SonarrSeries) arrItem {
			return arrItem{Name: series.Title, RootFolder: series.RootFolderPath, Monitored: series.Monitored}
		},
		workers: 4,
		sync: func(ctx context.Context, categoryID int64, series arrimport.SonarrSeries) (library.ArrImportCounts, error) {
			created, err := a.syncSeries(ctx, importID, categoryID, series, client, profileNames)
			switch {
			case err != nil:
				return library.ArrImportCounts{}, err
			case created:
				return library.ArrImportCounts{ArtistsCreated: 1}, nil
			}
			return library.ArrImportCounts{ArtistsMatched: 1}, nil
		},
	}
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
	ctx context.Context, importID, categoryID int64, series arrimport.SonarrSeries, client arrimport.SonarrClient,
	profileNames map[int64]string,
) (bool, error) {
	if series.TVDBID == 0 {
		return false, errors.New("no TVDB id")
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
		if kind := tv.SeriesType(series.SeriesType); kind == tv.SeriesAnime || kind == tv.SeriesDaily {
			if err := a.TV.SetSeriesType(ctx, result.WorkID, kind); err != nil {
				return false, fmt.Errorf("series type: %w", err)
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
