package book

import (
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/filekind"
)

var _ batch.Fitter = Importer{}

// Fit implements batch.Fitter: ebook files are a book's; sound is an audiobook's when one is an .m4b, else more likely an
// album's.
func (i Importer) Fit(unit inspect.Unit) float64 {
	want := filekind.Book
	if i.format().IsAudio() {
		want = filekind.Audio
	}
	fit := batch.FitCertain
	if want == filekind.Audio {
		fit = batch.FitPossible
	}
	for _, file := range unit.Media {
		if filekind.Of(file.RelPath) != want {
			return 0
		}
		if want == filekind.Audio && strings.EqualFold(path.Ext(file.RelPath), ".m4b") {
			fit = batch.FitCertain
		}
	}
	return fit
}
