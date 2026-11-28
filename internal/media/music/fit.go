package music

import (
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/filekind"
)

var _ batch.Fitter = Importer{}

// Fit implements batch.Fitter: a folder of sound alone is an album, unless an audiobook's own file is among it;
// a folder of video alone could be a concert film or clip, though a movie library usually holds it.
func (Importer) Fit(unit inspect.Unit) float64 {
	if isVideoUnit(unit) {
		return batch.FitPossible
	}
	fit := batch.FitLikely
	for _, file := range unit.Media {
		switch {
		case filekind.Of(file.RelPath) != filekind.Audio:
			return 0
		case strings.EqualFold(path.Ext(file.RelPath), ".m4b"):
			fit = batch.FitPossible
		}
	}
	return fit
}
