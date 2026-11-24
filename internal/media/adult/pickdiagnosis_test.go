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
