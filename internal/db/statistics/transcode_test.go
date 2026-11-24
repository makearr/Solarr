package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/statistics"
)

func TestTranscodes_RecordAddsPerDayAndATotalSumsLibrariesAndKinds(t *testing.T) {
	transcodes := statistics.NewTranscodes(openStatistics(t))
	day := statistics.DayOf(time.Date(2026, 11, 24, 15, 30, 0, 0, time.UTC))
	if day != time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("day = %d", day)
	}
	record := func(day int64, delta statistics.TranscodeDelta) {
		if err := transcodes.Record(t.Context(), day, delta); err != nil {
			t.Fatal(err)
		}
	}
	record(day, statistics.TranscodeDelta{CategoryID: 1, Kind: "video", Files: 1, OriginalBytes: 1000, ResultBytes: 400, EncodeSeconds: 60})
	record(day, statistics.TranscodeDelta{CategoryID: 1, Kind: "video", Files: 1, OriginalBytes: 500, ResultBytes: 300, EncodeSeconds: 30, Failed: 0})
	record(day+86400, statistics.TranscodeDelta{CategoryID: 1, Kind: "video", Failed: 1})
	record(day, statistics.TranscodeDelta{CategoryID: 2, Kind: "audio", Files: 3, OriginalBytes: 90, ResultBytes: 30, EncodeSeconds: 9})
	// A restore adds the negatives of what it undoes.
	record(day, statistics.TranscodeDelta{CategoryID: 2, Kind: "audio", Files: -1, OriginalBytes: -30, ResultBytes: -10, EncodeSeconds: -3})
	perLibrary, all, err := transcodes.Totals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []statistics.TranscodeTotal{
		{CategoryID: 1, Kind: "video", Files: 2, OriginalBytes: 1500, ResultBytes: 700, EncodeSeconds: 90, Failed: 1},
		{CategoryID: 2, Kind: "audio", Files: 2, OriginalBytes: 60, ResultBytes: 20, EncodeSeconds: 6},
	}
	if !reflect.DeepEqual(perLibrary, want) {
		t.Errorf("per library = %+v, want %+v", perLibrary, want)
	}
	if all.Files != 4 || all.OriginalBytes != 1560 || all.ResultBytes != 720 || all.EncodeSeconds != 96 || all.Failed != 1 {
		t.Errorf("all = %+v", all)
	}
	if empty, total, _ := statistics.NewTranscodes(openStatistics(t)).Totals(t.Context()); len(empty) != 0 || total != (statistics.TranscodeTotal{}) {
		t.Errorf("an empty table gave %+v %+v", empty, total)
	}
}
