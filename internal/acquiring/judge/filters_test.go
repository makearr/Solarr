package judge_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
)

func accepted() model.Decision { return model.Decision{Outcome: model.OutcomeAccept} }

func TestApplyFilters_Rules(t *testing.T) {
	const title = "Show.S01E02.1080p.WEB.H264-NTb"
	cases := []struct {
		name    string
		filters []judge.ReleaseFilter
		group   string
		ref     string
		reject  string
		delta   float64
	}{
		{name: "no filters accepts"},
		{name: "required word present", filters: []judge.ReleaseFilter{{Kind: judge.FilterMustContain, Value: "web"}}},
		{name: "any one required word is enough", filters: []judge.ReleaseFilter{
			{Kind: judge.FilterMustContain, Value: "remux"}, {Kind: judge.FilterMustContain, Value: "1080p"}}},
		{name: "required word missing", filters: []judge.ReleaseFilter{{Kind: judge.FilterMustContain, Value: "remux"}}, reject: "release_filter_required_word"},
		{name: "denied word", filters: []judge.ReleaseFilter{{Kind: judge.FilterMustNotContain, Value: "H264"}}, reject: "release_filter_denied_word"},
		{name: "denied pattern", filters: []judge.ReleaseFilter{{Kind: judge.FilterMustNotContain, Value: `h\.?26[45]`, IsRegex: true}}, reject: "release_filter_denied_word"},
		{name: "denied group", filters: []judge.ReleaseFilter{{Kind: judge.FilterGroupDeny, Value: "ntb"}}, group: "NTb", reject: "release_filter_denied_group"},
		{name: "allowed group", filters: []judge.ReleaseFilter{{Kind: judge.FilterGroupAllow, Value: "NTb"}}, group: "NTb"},
		{name: "group outside the allowed list", filters: []judge.ReleaseFilter{{Kind: judge.FilterGroupAllow, Value: "MeGusta"}}, group: "NTb", reject: "release_filter_allowed_group"},
		{name: "no group when groups are required", filters: []judge.ReleaseFilter{{Kind: judge.FilterGroupAllow, Value: "MeGusta"}}, reject: "release_filter_allowed_group"},
		{name: "preferred word scores", filters: []judge.ReleaseFilter{{Kind: judge.FilterPrefer, Value: "WEB", Score: 7}}, delta: 7},
		{name: "a rule for another season is skipped", filters: []judge.ReleaseFilter{{Prefix: "s02", Kind: judge.FilterMustNotContain, Value: "H264"}}},
		{name: "a rule for this season applies", filters: []judge.ReleaseFilter{{Prefix: "s01", Kind: judge.FilterMustNotContain, Value: "H264"}}, reject: "release_filter_denied_word"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := judge.ApplyFilters(accepted(), c.filters, title, c.group, "s01")
			if c.reject == "" {
				if got.Outcome != model.OutcomeAccept || got.Score() != c.delta {
					t.Errorf("outcome %s score %v, want ACCEPT %v (%+v)", got.Outcome, got.Score(), c.delta, got.Rejections)
				}
				return
			}
			var rules []string
			for _, rejection := range got.Rejections {
				rules = append(rules, rejection.Rule)
			}
			if got.Outcome != model.OutcomeReject || !reflect.DeepEqual(rules, []string{c.reject}) {
				t.Errorf("outcome %s rules %v, want REJECT [%s]", got.Outcome, rules, c.reject)
			}
		})
	}
}

func TestValidFilter(t *testing.T) {
	for filter, valid := range map[judge.ReleaseFilter]bool{
		{Kind: judge.FilterMustContain, Value: "x"}:                true,
		{Kind: judge.FilterMustContain, Value: "("}:                true,
		{Kind: judge.FilterMustContain, Value: "(", IsRegex: true}: false,
		{Kind: judge.FilterMustContain, Value: " "}:                false,
		{Kind: "nonsense", Value: "x"}:                             false,
	} {
		if got := judge.ValidFilter(filter) == ""; got != valid {
			t.Errorf("ValidFilter(%+v) valid = %v, want %v", filter, got, valid)
		}
	}
}
