package tv_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
)

// fakeOrderings is TheTVDB with a DVD order of the fixture show: the first two episodes trade places, the second
// season's first moves into the first, the rest close up.
type fakeOrderings struct{}

func (fakeOrderings) Series(context.Context, int64) (tvdb.Series, error) {
	return tvdb.Series{SeasonTypes: []tvdb.SeasonType{{Name: "Aired Order", Type: "official"}, {Name: "DVD Order", Type: "dvd"}}}, nil
}

func (fakeOrderings) Episodes(_ context.Context, _ int64, order string) ([]tvdb.Episode, error) {
	if order != "dvd" {
		return nil, errors.New("only the dvd order is faked")
	}
	return []tvdb.Episode{
		{ID: 1, SeasonNumber: 0, Number: 1}, {ID: 3, SeasonNumber: 1, Number: 1}, {ID: 2, SeasonNumber: 1, Number: 2},
		{ID: 4, SeasonNumber: 1, Number: 3}, {ID: 5, SeasonNumber: 2, Number: 1}, {ID: 6, SeasonNumber: 2, Number: 2},
	}, nil
}

// A series numbered by another of TheTVDB's orderings moves its episodes, files and all, and stays that way.
func TestApplyNumberingMovesTheEpisodesAndTheirFilesAndKeepsTheOrdering(t *testing.T) {
	cache := settingsCache(t)
	service, opened := newService(t, fakeSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: tvmaze.ErrNotFound})
	service.Reader, service.Orderings = library.NewSeriesReader(opened), fakeOrderings{}
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 7, ProfileID: profileID(t, cache, "Any"), Monitor: tv.MonitorAll})
	if err != nil {
		t.Fatal(err)
	}
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 9, []library.PhysicalFile{{RelPath: "Example Show/Season 1/e1.mkv", SizeBytes: 1, QuickHash: "e1", FirstSeenAt: addNow}})
	if err != nil {
		t.Fatal(err)
	}
	links := []library.SeriesFileLink{{MediaObjectID: registered[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 1)}}}}
	if _, err := library.NewSeries(opened).AttachFiles(t.Context(), addNow, added.WorkID, 9, links); err != nil {
		t.Fatal(err)
	}

	choices, current, err := service.NumberingChoices(t.Context(), added.WorkID)
	if err != nil || current != "tvdb:aired" || !reflect.DeepEqual(choices, []media.Choice{{Key: "tvdb:aired", Label: "Aired (TheTVDB)"}, {Key: "tvdb:dvd", Label: "DVD Order (TheTVDB)"}}) {
		t.Fatalf("choices = %+v, current %q, %v", choices, current, err)
	}
	if _, err := service.PlanNumbering(t.Context(), added.WorkID, "tvdb:regional"); !errors.Is(err, media.ErrUnknownNumbering) {
		t.Errorf("an ordering the series lacks: %v, want ErrUnknownNumbering", err)
	}
	plan, err := service.PlanNumbering(t.Context(), added.WorkID, "tvdb:dvd")
	if err != nil {
		t.Fatal(err)
	}
	want := media.NumberingPlan{
		Moves: []media.NumberingMove{
			{Title: "Episode", From: "S01E01", To: "S01E02", HasFile: true}, {Title: "Episode", From: "S01E02", To: "S01E01"},
			{Title: "Episode", From: "S02E01", To: "S01E03"}, {Title: "Episode", From: "S02E02", To: "S02E01"},
			{Title: "Episode", From: "S02E03", To: "S02E02"},
		},
		Unchanged: 1,
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v\nwant %+v", plan, want)
	}

	if _, err := service.ApplyNumbering(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 7, ProfileID: profileID(t, cache, "Any")}, "tvdb:dvd"); err != nil {
		t.Fatal(err)
	}
	refOfFile := func() string {
		t.Helper()
		rows, err := librarydb.New(opened.Read()).ListPartFilesForWork(t.Context(), added.WorkID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("files = %+v, %v", rows, err)
		}
		return rows[0].MediaRef
	}
	if got := refOfFile(); got != tv.MediaRef(1, 2) {
		t.Errorf("the file covers %s after the renumbering, want s01:e02 (its episode's DVD place)", got)
	}
	if _, err := service.Refresh(t.Context(), tv.RefreshRequest{WorkID: added.WorkID, CategoryID: 7, ProfileID: profileID(t, cache, "Any")}); err != nil {
		t.Fatal(err)
	}
	if got := refOfFile(); got != tv.MediaRef(1, 2) {
		t.Errorf("a refresh moved the file back to %s; the chosen ordering must hold", got)
	}
	part, err := librarydb.New(opened.Read()).FindWorkPart(t.Context(), librarydb.FindWorkPartParams{WorkID: added.WorkID, MediaRef: tv.MediaRef(1, 2)})
	if err != nil || part.Date.String != "2025-01-05" {
		t.Errorf("S01E02 after the refresh airs %q (%v); want 2025-01-05, its DVD episode's, not the aired order's", part.Date.String, err)
	}
	if monitoring := partMonitoring(t, opened, added.WorkID); len(monitoring) != 6 {
		t.Errorf("parts after the refresh = %v, want the six, none doubled", monitoring)
	}
	if _, current, _ := service.NumberingChoices(t.Context(), added.WorkID); current != "tvdb:dvd" {
		t.Errorf("current = %q, want tvdb:dvd", current)
	}
}
