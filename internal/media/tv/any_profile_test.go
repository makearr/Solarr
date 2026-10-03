package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/score"
)

// The shipped Any profile keeps a starved or bloated file behind its peers of the same resolution and source.
func TestAnyProfilePrefersASaneSizePerHour(t *testing.T) {
	cache := settingsCache(t)
	profile, found := cache.Snapshot().QualityProfile(profileID(t, cache, "Any"))
	if !found {
		t.Fatal("no Any profile")
	}
	rules, err := score.Parse([]byte(profile.RulesJSON))
	if err != nil {
		t.Fatal(err)
	}
	ranked := []judge.RankedProfile{{Name: profile.Name, Rules: rules}}
	scoreOf := func(bitrate int64) float64 {
		facts := score.Facts{File: score.FileFacts{Resolution: "1080p", Source: "webdl", VideoBitrate: bitrate}}
		return judge.EvaluateInOrder(ranked, facts, nil).Score()
	}
	sane, starved, bloated := scoreOf(4_000_000), scoreOf(100_000), scoreOf(40_000_000)
	if starved >= sane || bloated >= sane || sane-starved != 15 || sane-bloated != 15 {
		t.Errorf("scores sane %v, starved %v, bloated %v; want both 15 below the sane one", sane, starved, bloated)
	}
}
