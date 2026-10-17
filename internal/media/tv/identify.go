package tv

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNoEpisodes means a queued file's name does not say which episode it is and the person did not either.
var ErrNoEpisodes = errors.New("tv: the file's name does not say which episode it is; give the season and episode")

// ErrEpisodeUnknown means the series has no episode the file was said to be.
var ErrEpisodeUnknown = errors.New("tv: the series has no such episode")

// Identifier places a queued file by a person's decision: the series they chose and, when the name does not say, the
// season and episodes.
type Identifier struct {
	Service          Service
	Unidentified     library.Unidentified
	Clock            clock.Clock
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
}

// Resolve adds the series when the library does not hold it yet (an explicit choice lifts an exclusion), attaches the
// file to its episodes and takes it out of the queue for good. The episodes are the ones given, else those the file's
// own name states.
func (i Identifier) Resolve(ctx context.Context, categoryID int64, entry library.Row, ids IDs, season int, episodes []int) (int64, error) {
	keys := make([]library.PartKey, 0, len(episodes))
	for _, number := range episodes {
		keys = append(keys, library.PartKey{MediaRef: MediaRef(season, number)})
	}
	if len(keys) == 0 {
		info, ok := parsetv.ParsePath(entry.RelPath, i.Clock.Now())
		if keys = partKeys(info); !ok || len(keys) == 0 {
			return 0, ErrNoEpisodes
		}
	}
	workID, err := i.series(ctx, categoryID, ids)
	if err != nil {
		return 0, err
	}
	keepMonitoring := false
	if category, found := i.Categories.Category(categoryID); found {
		keepMonitoring = category.TVMonitor == settings.TVMonitorNone
	}
	result, err := i.Service.Series.AttachFiles(ctx, i.Clock.Now(), workID, entry.RootFolderID, []library.SeriesFileLink{
		{MediaObjectID: entry.MediaObjectID, Parts: keys, KeepMonitoring: keepMonitoring},
	})
	if err != nil {
		return 0, err
	}
	if len(result.Unresolved) > 0 {
		return 0, ErrEpisodeUnknown
	}
	if err := i.Unidentified.RecordManual(ctx, entry.MediaObjectID, workID, i.Clock.Now().Unix()); err != nil {
		return 0, err
	}
	if err := i.Unidentified.Clear(ctx, []int64{entry.MediaObjectID}); err != nil {
		return 0, err
	}
	return workID, nil
}

// series finds the work the ids name, or adds it with the library's own monitoring for a series a scan finds.
func (i Identifier) series(ctx context.Context, categoryID int64, ids IDs) (int64, error) {
	return findOrAddSeries(ctx, i.Service, i.Categories, i.DefaultProfileID, categoryID, ids)
}

// findOrAddSeries is the work the ids name: the one held, else added as a series a scan would have found.
func findOrAddSeries(
	ctx context.Context, service Service, categories interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}, defaultProfileID, categoryID int64, ids IDs,
) (int64, error) {
	provider, external := ProviderTVDB, ids.TVDB
	if external == 0 {
		provider, external = ProviderTMDB, ids.TMDB
	}
	if id, held, err := service.Series.WorkByProviderID(ctx, MediaType, provider, strconv.FormatInt(external, 10)); err != nil {
		return 0, err
	} else if held {
		return id, nil
	}
	profile := defaultProfileID
	monitor := MonitorExisting
	if category, found := categories.Category(categoryID); found {
		profile = categories.ProfileFor(category, profile)
		monitor = scanMonitor(category.TVMonitor)
	}
	added, err := service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: monitor})
	if err != nil {
		return 0, fmt.Errorf("tv: add the chosen series: %w", err)
	}
	return added.WorkID, nil
}
