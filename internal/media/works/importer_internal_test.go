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

func (r resolvedAs) ResolveByName(context.Context, batch.Request, inspect.Unit) (int64, []model.Identification) {
	return r.workID, nil
}

// A unit whose files resolve to another work than its names did is held for a person, not placed.
func TestImportBatch_AUnitWhoseNamesAndFilesDisagreeWaitsForAPerson(t *testing.T) {
	importer := NewImporter(ImporterConfig{Units: resolvedAs{workID: 41}})
	request := batch.Request{
		Units: []inspect.Unit{unitOf(10)}, UnitIDs: []int64{1},
		Names: []batch.NameMatch{{WorkID: 7, Confidence: 1}},
	}
	result, err := importer.ImportBatch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Decisions[0]; got.Decision != library.UnitQueue || got.Reason != batch.ReasonNameDisagrees || result.Placements[0] != nil {
		t.Errorf("decision = %+v placement = %v, want queued as %q", got, result.Placements[0], batch.ReasonNameDisagrees)
	}
}

// The name stage shows a resolved work at full confidence and nothing for a unit its names cannot place.
func TestNameMatches_ShowTheWorkTheNamesResolveTo(t *testing.T) {
	matches := NewImporter(ImporterConfig{Units: resolvedAs{workID: 41}}).NameMatches(t.Context(), batch.Request{Units: []inspect.Unit{unitOf(10)}})
	if len(matches) != 1 || matches[0].WorkID != 41 || !matches[0].Matched() {
		t.Errorf("matches = %+v, want work 41 matched", matches)
	}
	none := NewImporter(ImporterConfig{Units: resolvedAs{}}).NameMatches(t.Context(), batch.Request{Units: []inspect.Unit{unitOf(10)}})
	if none[0].Matched() {
		t.Errorf("a unit the names cannot place matched: %+v", none[0])
	}
}
