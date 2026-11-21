package score

import (
	"reflect"
	"testing"
)

func TestValidateStrict(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []Problem
	}{
		{"clean", `{"compare":["bitrate_distance","score"],"targets":{"bitrate":[{"when":{"audio_codec":"mp3"},"min":"192kbps","ideal":"320kbps","max":"400kbps"}]}}`, nil},
		{"unknown key", `{"compare":["score"],"require":{"reject_codec":["xvid"]}}`,
			[]Problem{{Path: "", Value: "reject_codec", Expectation: "a known key (see the profile reference); this one would be ignored"}}},
		{"bare bitrate", `{"targets":{"bitrate":[{"ideal":"320"}]}}`,
			[]Problem{{Path: "targets.bitrate[0].ideal", Value: "320", Expectation: "a rate with its unit: 320kbps, 4Mbps, 40MB/min"}}},
		{"missing ideal", `{"targets":{"bit_depth":[{}]}}`,
			[]Problem{{Path: "targets.bit_depth[0].ideal", Expectation: "a value with its unit, such as 320kbps"}}},
		{"min above ideal", `{"targets":{"sample_rate":[{"min":"96kHz","ideal":"48kHz"}]}}`,
			[]Problem{{Path: "targets.sample_rate[0].min", Value: "96kHz", Expectation: "not above ideal"}}},
		{"max below ideal", `{"targets":{"sample_rate":[{"ideal":"48kHz","max":"44100"}]}}`,
			[]Problem{{Path: "targets.sample_rate[0].max", Value: "44100", Expectation: "not below ideal"}}},
		{"lenience", `{"targets":{"bit_depth":[{"ideal":"16","lenience":5}]}}`,
			[]Problem{{Path: "targets.bit_depth[0].lenience", Value: "5", Expectation: "a share from 0 up to, not including, 1"}}},
		{"negative weight", `{"targets":{"bit_depth":[{"ideal":"16","weights":{"under":-1}}]}}`,
			[]Problem{{Path: "targets.bit_depth[0].weights.under", Value: "-1", Expectation: "zero or more"}}},
		{"misspelt compare", `{"compare":["bitrat_distance"]}`,
			[]Problem{{Path: "compare[0]", Value: "bitrat_distance", Expectation: "score, lossless, a field name, a tiers dimension or <number field>_distance"}}},
		{"bad regex", `{"scoring":[{"name":"x","score":1,"specs":[{"field":"title","matches":"([bad"}]}]}`,
			[]Problem{{Path: "scoring[0].specs[0].matches", Value: "([bad", Expectation: "a valid regular expression"}}},
		{"upper case codec", `{"require":{"reject_codecs":["XviD"]}}`,
			[]Problem{{Path: "require.reject_codecs[0]", Value: "XviD", Expectation: "lower case, as the probe reports codecs"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidateStrict([]byte(test.json)); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ValidateStrict = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestValidateStrict_UpgradeBelongsBesideTheRules(t *testing.T) {
	got := ValidateStrict([]byte(`{"compare":["score"],"upgrade":{"min_delta":15}}`))
	want := []Problem{{Path: "", Value: "upgrade", Expectation: "not here: the upgrade policy is set in its own field beside the rules"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ValidateStrict = %#v, want %#v", got, want)
	}
}

func TestValidateUpgradeStrict(t *testing.T) {
	if got := ValidateUpgradeStrict([]byte(`{"min_delta":2,"window":"24h"}`)); got != nil {
		t.Errorf("a known document: %#v, want none", got)
	}
	if got := ValidateUpgradeStrict(nil); got != nil {
		t.Errorf("an empty document: %#v, want none", got)
	}
	want := []Problem{{Value: "min_delt", Expectation: "a known upgrade key; this one would be ignored"}}
	if got := ValidateUpgradeStrict([]byte(`{"min_delt":2}`)); !reflect.DeepEqual(got, want) {
		t.Errorf("a typo: %#v, want %#v", got, want)
	}
}
