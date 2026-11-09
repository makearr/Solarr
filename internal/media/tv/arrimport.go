package tv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
)

// seriesAdder is the service's add, which a Sonarr import maps each series through.
type seriesAdder interface {
	Add(ctx context.Context, request AddRequest) (AddResult, error)
	SetEpisodeMonitoring(ctx context.Context, categoryID, workID int64, wanted map[Key]bool) error
	SetProfile(ctx context.Context, categoryID, workID int64, profileID *int64) error
	SetSeriesType(ctx context.Context, workID int64, kind SeriesType) error
}

// SonarrImport adds Sonarr's series, four at once, each by its TVDB id. A series Solarr already holds is matched and left
// as it is; one it creates takes Sonarr's monitored flags, series and season. Series count in the run's first-kind tally.
type SonarrImport struct {
	Adder   seriesAdder
	Cache   *settings.Cache
	Desired library.Desired
	// ProfileFor is a library's profile for what its import adds.
	ProfileFor func(category settings.Category) int64
	// Warm reads in bulk what Wikidata says of the series about to be added; nil warms nothing.
	Warm     func(ctx context.Context, ids []string)
	Monitors arrmap.Monitors
	Logger   *slog.Logger
}

// Import walks Sonarr's series.
func (s SonarrImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	client := run.Client.Sonarr()
	var profileNames map[int64]string
	return arrmap.Walk(ctx, run, arrmap.Source[arrimport.SonarrSeries]{
		Connect: func(ctx context.Context) error {
			if s.Adder == nil {
				return errors.New("no TV service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		List: func(ctx context.Context) ([]arrimport.SonarrSeries, error) {
			profileNames = sonarrProfileNames(ctx, client)
			return client.Series(ctx)
		},
		Prepare: func(ctx context.Context, chosen []arrimport.SonarrSeries) {
			if s.Warm == nil {
				return
			}
			ids := make([]string, 0, len(chosen))
			for _, one := range chosen {
				if one.TVDBID != 0 {
					ids = append(ids, strconv.FormatInt(one.TVDBID, 10))
				}
			}
			s.Warm(ctx, ids)
		},
		Describe: func(series arrimport.SonarrSeries) arrmap.Item {
			return arrmap.Item{Name: series.Title, RootFolder: series.RootFolderPath, Monitored: series.Monitored, HasFiles: series.HasFiles()}
		},
		Workers: 4,
		Monitor: func(ctx context.Context, _ int64, series arrimport.SonarrSeries) (library.ArrImportCounts, error) {
			return s.monitorSeries(ctx, run, series, client)
		},
		Sync: func(ctx context.Context, categoryID int64, series arrimport.SonarrSeries) (library.ArrImportCounts, error) {
			return arrmap.Tally(s.syncSeries(ctx, run, categoryID, series, client, profileNames))
		},
	})
}

func sonarrProfileNames(ctx context.Context, client arrimport.SonarrClient) map[int64]string {
	return arrmap.ProfileNames(ctx, client.QualityProfiles, func(profile arrimport.SonarrQualityProfile) (int64, string) {
		return profile.ID, profile.Name
	})
}

func (s SonarrImport) syncSeries(
	ctx context.Context, run arrmap.Run, categoryID int64, series arrimport.SonarrSeries, client arrimport.SonarrClient, profileNames map[int64]string,
) (bool, error) {
	if series.TVDBID == 0 {
		return false, errors.New("no TVDB id")
	}
	category, _ := s.Cache.Snapshot().Category(categoryID)
	monitor := MonitorNone
	if series.Monitored {
		monitor = MonitorAll
	}
	result, err := s.Adder.Add(ctx, AddRequest{
		IDs:        IDs{TVDB: series.TVDBID, TVmaze: series.TVMazeID, TMDB: series.TMDBID, IMDb: series.IMDbID},
		CategoryID: categoryID, ProfileID: s.ProfileFor(category),
		Monitor: monitor, Specials: true,
	})
	if err != nil {
		return false, err
	}
	if result.Created {
		if err := s.carryOverMonitoring(ctx, categoryID, result.WorkID, series, client); err != nil {
			return false, err
		}
		s.carryOverProfile(ctx, categoryID, result.WorkID, series, profileNames, run.Options.ProfileMap)
		if kind := SeriesType(series.SeriesType); kind == SeriesAnime || kind == SeriesDaily {
			if err := s.Adder.SetSeriesType(ctx, result.WorkID, kind); err != nil {
				return false, fmt.Errorf("series type: %w", err)
			}
		}
	}
	workID := result.WorkID
	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemSeries, SourceID: strconv.FormatInt(series.ID, 10), Title: series.Title,
		WorkID: &workID, WorkCreated: result.Created,
	}); err != nil {
		return false, err
	}
	run.Link(ctx, arrmap.LinkSeries, series.ID, "work", workID)
	return result.Created, nil
}

// sonarrEpisodes is the one call carryOverMonitoring makes of the Sonarr client.
type sonarrEpisodes interface {
	Episodes(ctx context.Context, seriesID int64) ([]arrimport.SonarrEpisode, error)
}

// carryOverMonitoring gives a series Solarr just created the monitoring Sonarr holds, episode by episode; when the
// episode list cannot be read, the season flags stand in.
func (s SonarrImport) carryOverMonitoring(ctx context.Context, categoryID, workID int64, series arrimport.SonarrSeries, client sonarrEpisodes) error {
	if episodes, err := client.Episodes(ctx, series.ID); err == nil && len(episodes) > 0 {
		wanted := make(map[Key]bool, len(episodes))
		for _, episode := range episodes {
			wanted[Key{Season: episode.SeasonNumber, Number: episode.EpisodeNumber}] = series.Monitored && episode.Monitored
		}
		return s.Adder.SetEpisodeMonitoring(ctx, categoryID, workID, wanted)
	}
	if !series.Monitored {
		return nil
	}
	for _, season := range series.Seasons {
		if season.Monitored {
			continue
		}
		if _, err := s.Desired.SetSeasonMonitored(ctx, workID, SeasonRef(season.SeasonNumber), false); err != nil {
			return fmt.Errorf("season %d: %w", season.SeasonNumber, err)
		}
	}
	return nil
}

// carryOverProfile gives a created series the Solarr profile the import's mapping names for its Sonarr profile, nil
// keeping the library's; unmapped, the TV profile of the same name, when one has it.
func (s SonarrImport) carryOverProfile(
	ctx context.Context, categoryID, workID int64, series arrimport.SonarrSeries, profileNames map[int64]string, profileMap map[int64]*int64,
) {
	id, mapped := profileMap[series.QualityProfileID]
	if !mapped {
		id = arrmap.SameNameProfile(s.Cache.Snapshot().QualityProfiles(), MediaType, profileNames[series.QualityProfileID])
	}
	if id == nil {
		return
	}
	if err := s.Adder.SetProfile(ctx, categoryID, workID, id); err != nil && s.Logger != nil {
		s.Logger.Warn("sonarr import: quality profile not carried over", "series", series.Title, "error", err)
	}
}

// monitorSeries gives the series Solarr holds under its TheTVDB id the monitoring Sonarr holds, episode by episode.
func (s SonarrImport) monitorSeries(ctx context.Context, run arrmap.Run, series arrimport.SonarrSeries, client sonarrEpisodes) (library.ArrImportCounts, error) {
	if series.TVDBID == 0 {
		return arrmap.Skipped, nil
	}
	workID, held, err := s.Monitors.Series.WorkByProviderID(ctx, MediaType, ProviderTVDB, strconv.FormatInt(series.TVDBID, 10))
	if err != nil || !held {
		return arrmap.Found(false, err)
	}
	items, err := s.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil || len(items) == 0 {
		return arrmap.Found(false, err)
	}
	run.Link(ctx, arrmap.LinkSeries, series.ID, "work", workID)
	return arrmap.Found(true, s.carryOverMonitoring(ctx, items[0].CategoryID, workID, series, client))
}

// Preview reads Sonarr's series.
func (s SonarrImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	sonarr := client.Sonarr()
	source := arrmap.PreviewSource{ItemKind: "series", MediaType: MediaType}
	series, err := sonarr.Series(ctx)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	for _, one := range series {
		source.Items = append(source.Items, arrmap.PreviewItem{RootFolder: one.RootFolderPath, Monitored: one.Monitored,
			ProfileID: one.QualityProfileID, Provider: ProviderTVDB, ExternalID: arrmap.IDText(one.TVDBID)})
	}
	return arrmap.ReadPreview(ctx, source, sonarr, sonarrProfileNames(ctx, sonarr))
}
