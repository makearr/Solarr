package works

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/importing/inspect"
)

func unitOf(size int64) inspect.Unit {
	return inspect.Unit{Media: []inspect.File{{SizeBytes: size / 2}, {SizeBytes: size}}}
}

// A film download with an extras folder that sorts first must still place the film.
func TestVisitOrder_ASeededDownloadTriesItsLargestUnitFirst(t *testing.T) {
	units := []inspect.Unit{unitOf(25), unitOf(900), unitOf(25)}

	if got, want := visitOrder(units, true), []int{1, 0, 2}; !slices.Equal(got, want) {
		t.Errorf("seeded order = %v, want %v", got, want)
	}
	if got, want := visitOrder(units, false), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Errorf("unseeded order = %v, want request order %v", got, want)
	}
}
