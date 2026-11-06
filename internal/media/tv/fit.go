package tv

import (
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

var _ batch.Fitter = Importer{}

// Fit implements batch.Fitter: a video named as an episode is one; any other video only when nothing else takes it.
func (i Importer) Fit(unit inspect.Unit) float64 {
	feature, ok := inspect.LargestVideo(unit)
	if !ok {
		return 0
	}
	if _, parsed := parsetv.ParsePath(feature.RelPath, i.config.Clock.Now()); parsed {
		return batch.FitCertain
	}
	return batch.FitFallback
}
