package works

import (
	"context"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

type resolvedAs struct {
	UnitPlacer
	workID int64
}

func (r resolvedAs) Resolve(context.Context, batch.Request, inspect.Unit) (int64, []model.Identification) {
	return r.workID, nil
}

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

// A preview holds a unit that resolved to a work for a person, and says it resolved: confidence 1, the work named.
func TestImportBatch_APreviewHoldsAResolvedUnitAtFullConfidence(t *testing.T) {
	importer := NewImporter(ImporterConfig{Units: resolvedAs{workID: 41}})
	result, err := importer.ImportBatch(t.Context(), batch.Request{Units: []inspect.Unit{unitOf(10)}, UnitIDs: []int64{1}, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	workID := int64(41)
	want := library.UnitDecision{UnitID: 1, Decision: library.UnitQueue, Reason: batch.ReasonPreview, Confidence: 1, WorkID: &workID}
	if got := result.Decisions[0]; got.UnitID != want.UnitID || got.Decision != want.Decision || got.Reason != want.Reason ||
		got.Confidence != want.Confidence || got.WorkID == nil || *got.WorkID != workID {
		t.Errorf("decision = %+v, want %+v", got, want)
	}
}
