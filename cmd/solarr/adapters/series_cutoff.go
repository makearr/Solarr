package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/identify/quality"
)

// CutoffUnmet lists the present episodes whose measured file is below the library's target or cutoff, in series order.
func (c SeriesCatalog) CutoffUnmet(ctx context.Context, categoryID int64, offset, limit int) ([]gen.WantedEpisode, bool, error) {
	category, found := c.Categories.Category(categoryID)
	if !found || (category.QualityTarget == nil && category.QualityCutoffID == nil) {
		return []gen.WantedEpisode{}, false, nil
	}
	held, err := c.Reader.HeldEpisodes(ctx, categoryID)
	if err != nil {
		return nil, true, err
	}
	snapshot := c.Categories.Cache.Snapshot()
	below := make([]gen.WantedEpisode, 0, limit)
	skipped := 0
	for _, episode := range held {
		facts := fileFacts(episode.File)
		judged, ok := quality.Judge(snapshot, category, facts)
		if !ok || judged.MeetsCutoff {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		view := gen.WantedEpisode{PartId: episode.PartID, SeriesId: episode.WorkID, SeriesTitle: episode.SeriesTitle, Season: episode.Season, Number: episode.Number}
		setString(&view.Title, episode.Title)
		setString(&view.AirDate, episode.Date)
		view.File = episodeFileView(episode.File, category, true)
		below = append(below, view)
		if len(below) == limit {
			break
		}
	}
	return below, true, nil
}
