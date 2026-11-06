package movie

import (
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

var _ batch.Fitter = Importer{}

// Fit implements batch.Fitter: a video named with a title and a year is a film; any other video possibly one.
func (Importer) Fit(unit inspect.Unit) float64 {
	feature, ok := inspect.LargestVideo(unit)
	if !ok {
		return 0
	}
	if info, parsed := parsemovie.ParsePath(feature.RelPath); parsed && info.Year > 0 {
		return batch.FitLikely
	}
	return batch.FitPossible
}
