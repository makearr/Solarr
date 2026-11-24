package adult

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parseadult"
)

func hit(title, studio, date string, rank int) Candidate {
	return Candidate{Title: title, Studio: studio, Date: date, Rank: rank, IDs: IDs{StashDB: title + date}}
}

func TestDiagnosePick_NamesTheGateThatRejectedTheBestTitleMatch(t *testing.T) {
	file := parseadult.Info{Studio: "Nimbus Studio", Title: "Harbour Scene", Date: "2024-03-05", Year: 2024}
	for name, test := range map[string]struct {
		candidates []Candidate
		info       parseadult.Info
		want       pickDiagnosis
	}{
		"no hits":                       {nil, file, pickDiagnosis{Reason: "no_candidates"}},
		"title differs":                 {[]Candidate{hit("Other Scene", "Nimbus Studio", "2024-03-05", 0)}, file, pickDiagnosis{Reason: "title_mismatch"}},
		"file has no day":               {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-05", 0)}, parseadult.Info{Title: "Harbour Scene", Year: 2024}, pickDiagnosis{Reason: "file_date_missing", TitleMatches: 1}},
		"hit has no day":                {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "", 1)}, file, pickDiagnosis{Reason: "provider_date_missing", Rank: 1, TitleMatches: 1}},
		"ranked too low":                {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-09", 4)}, file, pickDiagnosis{Reason: "rank_too_low", Rank: 4, DaysOff: 4, TitleMatches: 1}},
		"studio lists it on other days": {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-02", 0), hit("Harbour Scene", "Nimbus Studio", "2024-03-20", 1)}, file, pickDiagnosis{Reason: "sibling_days", DaysOff: 3, TitleMatches: 2}},
		"day too far":                   {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-12", 0)}, file, pickDiagnosis{Reason: "date_off", DaysOff: 7, TitleMatches: 1}},
		"another studio":                {[]Candidate{hit("Harbour Scene", "Cirrus Studio", "2024-03-12", 0)}, file, pickDiagnosis{Reason: "date_off", DaysOff: 7, TitleMatches: 1, StudioDiffers: true}},
		"two on its day":                {[]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-05", 0), hit("Harbour Scene", "Cirrus Studio", "2024-03-05", 1)}, file, pickDiagnosis{Reason: "several_named", TitleMatches: 2}},
	} {
		if got := diagnosePick(test.candidates, test.info); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %+v, want %+v", name, got, test.want)
		}
	}
}

func TestLogPick_IsOneStructuredDebugLineAndSilentAtInfo(t *testing.T) {
	file := parseadult.Info{Studio: "Nimbus Studio", Title: "Harbour Scene", Date: "2024-03-05"}
	hits := []Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-12", 0)}
	for _, test := range []struct {
		level slog.Level
		lines int
	}{{slog.LevelDebug, 1}, {slog.LevelInfo, 0}} {
		var out bytes.Buffer
		resolver := Resolver{Logger: slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: test.level}))}
		resolver.logPick(file, hits, "")
		if got := strings.Count(out.String(), `msg="adult pick"`); got != test.lines {
			t.Errorf("level %v: %d lines, want %d: %s", test.level, got, test.lines, out.String())
		}
		if test.lines == 1 && !strings.Contains(out.String(), "reason=date_off") {
			t.Errorf("line = %s, want reason=date_off", out.String())
		}
	}
}

func TestIdentifications_CarryTheDateAndStudioOfEachHit(t *testing.T) {
	got := Resolver{}.identifications([]Candidate{hit("Harbour Scene", "Nimbus Studio", "2024-03-05", 0), hit("Quiet Pier", "", "", 1)})
	want := [][]model.MatchEvidence{
		{{Kind: "parse", Detail: "Harbour Scene (Nimbus Studio)"}, {Kind: "date", Detail: "2024-03-05"}, {Kind: "studio", Detail: "Nimbus Studio"}},
		{{Kind: "parse", Detail: "Quiet Pier"}},
	}
	for index, identification := range got {
		if !reflect.DeepEqual(identification.Evidence, want[index]) {
			t.Errorf("hit %d: evidence %+v, want %+v", index, identification.Evidence, want[index])
		}
	}
}

func TestPickCandidate_AHitNumberedOtherwiseThanTheFileIsNotTaken(t *testing.T) {
	file := parseadult.Info{Studio: "Nimbus Studio", Title: "Harbour Scene", Date: "2024-03-05", Year: 2024, Season: 41, Episode: 3}
	wrong := hit("Harbour Scene - S41:E2", "Nimbus Studio", "2024-03-05", 0)
	right := hit("Harbour Scene - S 41:E 3", "Nimbus Studio", "2024-03-05", 1)
	plain := hit("Harbour Scene", "Nimbus Studio", "2024-03-05", 2)

	if _, _, ok := pickCandidate([]Candidate{wrong}, file); ok {
		t.Error("the only hit is numbered S41:E2 for a file S41E3: taken")
	}
	if got, _, ok := pickCandidate([]Candidate{wrong, right}, file); !ok || got.IDs != right.IDs {
		t.Errorf("wrong then right: picked %+v (%v), want the S41:E3 hit", got.Title, ok)
	}
	// Same title, same day, same studio: the agreeing number breaks the tie against a hit with none.
	if got, _, ok := pickCandidate([]Candidate{plain, right}, file); !ok || got.IDs != right.IDs {
		t.Errorf("plain then right: picked %+v (%v), want the numbered hit that agrees", got.Title, ok)
	}
	// A hit with no numbering is judged as before, and a file with none ignores the hit's.
	if got, _, ok := pickCandidate([]Candidate{plain}, file); !ok || got.IDs != plain.IDs {
		t.Errorf("plain only: picked %+v (%v), want it", got.Title, ok)
	}
	bare := parseadult.Info{Studio: "Nimbus Studio", Title: "Harbour Scene", Date: "2024-03-05", Year: 2024}
	if got, _, ok := pickCandidate([]Candidate{wrong}, bare); !ok || got.IDs != wrong.IDs {
		t.Errorf("file without numbering: picked %+v (%v), want the hit", got.Title, ok)
	}
	if got := diagnosePick([]Candidate{wrong}, file); got.Reason != "numbering_differs" {
		t.Errorf("diagnosis = %+v, want numbering_differs", got)
	}
}
