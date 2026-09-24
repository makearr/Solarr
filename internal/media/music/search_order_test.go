package music

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/score"
)

// The shipped profiles' rules (settings migration 0012), as the library's ordered list.
const (
	spaceConsciousRules = `{"quality_profile":"Space conscious","compare":["audio_codec","bitrate_distance","score"],` +
		`"tiers":{"audio_codec":[["opus"],["aac"],["ogg"],["mp3"],["flac","alac"]]},"targets":{"bitrate":[` +
		`{"when":{"audio_codec":"opus"},"min":"96kbps","ideal":"128kbps","max":"160kbps"}]}}`
	lossy320Rules = `{"quality_profile":"Lossy 320","compare":["audio_codec","bitrate_distance","score"],` +
		`"tiers":{"audio_codec":[["mp3","aac","ogg","opus"],["flac","alac"]]},"targets":{"bitrate":[` +
		`{"when":{"audio_codec":"mp3"},"min":"256kbps","ideal":"320kbps","max":"320kbps"}]}}`
)

func TestEvaluateInOrder_TheEarliestFittingProfileRanksFirst(t *testing.T) {
	var profiles []rankedProfile
	for name, raw := range map[string]string{"Space conscious": spaceConsciousRules, "Lossy 320": lossy320Rules} {
		rules, err := score.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, rankedProfile{name: name, rules: rules})
	}
	if profiles[0].name != "Space conscious" {
		profiles[0], profiles[1] = profiles[1], profiles[0]
	}
	release := func(codec string, kbps int64) score.Facts {
		return score.Facts{File: score.FileFacts{AudioCodec: codec, AudioBitrate: kbps * 1000, OverallBitrate: kbps * 1000}}
	}

	for _, tc := range []struct {
		name  string
		facts score.Facts
		rank  int
		label string
	}{
		{"opus 128 fits the first", release("opus", 128), 0, "Space conscious"},
		{"mp3 320 fits the second", release("mp3", 320), 1, "Lossy 320"},
		{"flac fits neither, ranked after every fit", release("flac", 900), 2, "Space conscious"},
	} {
		decision := evaluateInOrder(profiles, tc.facts, nil)
		lead := decision.Quality.Tiers[0]
		if lead.Dimension != "profile_priority" || lead.Rank != tc.rank || lead.Label != tc.label {
			t.Errorf("%s: leading tier = %+v, want rank %d by %s", tc.name, lead, tc.rank, tc.label)
		}
		if decision.Score() != score.Evaluate(profiles[0].rules, tc.facts, nil).Score() &&
			decision.Score() != score.Evaluate(profiles[1].rules, tc.facts, nil).Score() {
			t.Errorf("%s: the priority component changed the score to %v", tc.name, decision.Score())
		}
	}
}
