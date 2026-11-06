package adult

import (
	"path"
	"regexp"

	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

var _ batch.Fitter = Importer{}

// adultMarker is the token a scene release names itself with.
var adultMarker = regexp.MustCompile(`(?i)(?:^|[-._ \[(])XXX(?:$|[-._ \])])`)

// Fit implements batch.Fitter: a video whose name says XXX is a scene, one named by its day as a scene is likely one,
// and no other video is taken for one.
func (Importer) Fit(unit inspect.Unit) float64 {
	feature, ok := inspect.LargestVideo(unit)
	if !ok {
		return 0
	}
	if adultMarker.MatchString(feature.RelPath) {
		return batch.FitDecisive
	}
	base := path.Base(feature.RelPath)
	if info, parsed := parseadult.ParseName(base[:len(base)-len(path.Ext(base))]); parsed && info.Date != "" {
		return batch.FitCertain
	}
	return 0
}
