package tv

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// RenameSeries answers a series with its episodes and their files. Satisfied by library.SeriesReader.
type RenameSeries interface {
	Get(ctx context.Context, categoryID, workID int64, now time.Time) (library.SeriesDetailRow, error)
}

// RenameConfig is what naming a TV library's files needs (Sonarr's Rename Episodes); renamejob.Engine does the rest.
type RenameConfig struct {
	Series RenameSeries
	Clock  clock.Clock
}

// NameFiles names a series' files from their episodes and release name, the probe filling what the name no longer
// says; a file whose episodes the series does not list keeps its name.
func (cfg RenameConfig) NameFiles(
	ctx context.Context, category settings.Category, workID int64, files []renamejob.File,
) ([]renamejob.Named, error) {
	now := cfg.Clock.Now()
	detail, err := cfg.Series.Get(ctx, category.ID, workID, now)
	if err != nil {
		return nil, fmt.Errorf("tv: series %d: %w", workID, err)
	}
	episodes := make(map[string]library.SeriesEpisode, len(detail.Episodes))
	for _, episode := range detail.Episodes {
		episodes[MediaRef(episode.Season, episode.Number)] = episode
	}
	names := NamingFor(category.Naming)
	named := make([]renamejob.Named, 0, len(files))
	for _, file := range files {
		var parts []library.ResolvedPart
		var probed *library.EpisodeFile
		for _, ref := range file.MediaRefs {
			episode, found := episodes[ref]
			if !found {
				continue
			}
			parts = append(parts, library.ResolvedPart{PartID: episode.PartID, MediaRef: ref, Title: episode.Title, Date: episode.Date})
			if episode.File != nil && episode.File.MediaObjectID == file.MediaObjectID {
				probed = episode.File
			}
		}
		if len(parts) == 0 {
			continue
		}
		info, _ := parsetv.ParsePath(file.RelPath, now)
		episode := withProbe(episodeNaming(detail, fileEpisodes{
			media: inspect.File{RelPath: file.RelPath, SizeBytes: file.SizeBytes}, info: info, parts: parts,
		}), probed)
		rendered, warnings, err := names.RenderEpisode(episode, file.RelPath)
		if err != nil {
			continue
		}
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: rendered, Warnings: warnings})
	}
	return named, nil
}

// withProbe fills what the file's name no longer tells from what the probe measured of the file.
func withProbe(episode EpisodeNaming, probed *library.EpisodeFile) EpisodeNaming {
	if probed == nil {
		return episode
	}
	if episode.QualityTitle == "" {
		episode.QualityTitle = probed.Resolution
	}
	if episode.VideoCodec == "" {
		episode.VideoCodec = strings.ToUpper(probed.VideoCodec)
	}
	if episode.DynamicRange == "" {
		episode.DynamicRange = probed.HDR
	}
	if episode.AudioCodec == "" {
		episode.AudioCodec = strings.ToUpper(probed.AudioCodec)
	}
	if episode.AudioChannels == "" {
		episode.AudioChannels = probed.AudioChannels
	}
	return episode
}
