package book

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

func TestImporter_Fit(t *testing.T) {
	ebooks := NewImporter(ImporterConfig{Resolver: Resolver{Service: Service{Format: Ebook}}})
	audiobooks := NewImporter(ImporterConfig{Resolver: Resolver{Service: Service{Format: Audiobook}}})
	for name, tc := range map[string]struct {
		importer Importer
		files    []string
		want     float64
	}{
		"an ebook":                     {ebooks, []string{"A/book.epub"}, batch.FitCertain},
		"an ebook beside sound":        {ebooks, []string{"A/book.pdf", "A/01.mp3"}, 0},
		"an audiobook's own file":      {audiobooks, []string{"A/book.m4b"}, batch.FitCertain},
		"sound an album usually holds": {audiobooks, []string{"A/01.mp3"}, batch.FitPossible},
		"an ebook to audiobooks":       {audiobooks, []string{"A/book.epub"}, 0},
	} {
		unit := inspect.Unit{}
		for _, file := range tc.files {
			unit.Media = append(unit.Media, inspect.File{RelPath: file})
		}
		if got := tc.importer.Fit(unit); got != tc.want {
			t.Errorf("%s: fit %v, want %v", name, got, tc.want)
		}
	}
}
