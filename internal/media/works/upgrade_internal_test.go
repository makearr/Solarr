package works

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestAnUpgradePackMostlyOfGoodEpisodesIsRejected(t *testing.T) {
	yard := yardstick[struct{}]{rules: []judge.RankedProfile{{Name: "any"}}, incumbents: map[int]model.Decision{1: {}}}
	for _, test := range []struct {
		numbers  []int
		held     int
		rejected bool
	}{
		{[]int{1}, 1, false},
		{[]int{1}, 10, true},
		{[]int{1, 2, 3, 4, 5}, 10, false},
		{[]int{1, 2, 3, 4}, 10, true},
	} {
		decision := model.Decision{Outcome: model.OutcomeAccept}
		yard.requireUpgrade(&decision, Release{Parts: test.numbers, Held: test.held})
		packRejected := decision.Outcome == model.OutcomeReject && len(decision.Rejections) > 0 && decision.Rejections[0].Field == "pack"
		if packRejected != test.rejected {
			t.Errorf("covering %v of %d episodes: rejected for its size = %v, want %v", test.numbers, test.held, packRejected, test.rejected)
		}
	}
}

func TestAWholeWorkIsUpgradedOnlyByABetterFile(t *testing.T) {
	yard := yardstick[struct{}]{rules: []judge.RankedProfile{{Name: "any"}}, incumbents: map[int]model.Decision{0: {}}}
	decision := model.Decision{Outcome: model.OutcomeAccept}
	yard.requireUpgrade(&decision, Release{})
	if decision.Outcome != model.OutcomeReject || decision.Rejections[0].Wanted != "better than the file you have" {
		t.Errorf("a tie must keep the file owned: %+v", decision)
	}
	none := yardstick[struct{}]{rules: yard.rules}
	filling := model.Decision{Outcome: model.OutcomeAccept}
	none.requireUpgrade(&filling, Release{})
	if filling.Outcome != model.OutcomeAccept {
		t.Errorf("a search filling a gap rejects nothing for quality: %+v", filling)
	}
}

func TestUpgradeNewest_AskedForSearchIgnoresTheWait(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	if got, want := UpgradeNewest(t.Context(), now), now.Add(-UpgradeMinFileAge).Unix(); got != want {
		t.Errorf("automatic search newest = %d, want %d", got, want)
	}
	if got, want := UpgradeNewest(AskedFor(t.Context()), now), now.Unix(); got != want {
		t.Errorf("asked-for search newest = %d, want %d", got, want)
	}
}

// What a file's grab recorded is compared, so a better source or a repack replaces it; a scanned file keeps today's rule (QUAL-1).
func TestRequireUpgrade_ComparesWhatTheGrabRecorded(t *testing.T) {
	rules := []judge.RankedProfile{{Name: "sources", Rules: score.Profile{
		Compare: []string{"source"}, Tiers: map[string][][]string{"source": {{"bluray"}, {"webdl"}}},
	}}}
	owned := func(known bool) (map[int]model.Decision, map[int]score.FileFacts) {
		file := score.FileFacts{ReleaseKnown: known}
		if known {
			file.Source = "webdl"
		}
		return map[int]model.Decision{0: judge.EvaluateInOrder(rules, score.Facts{File: file}, nil)}, map[int]score.FileFacts{0: file}
	}
	for name, test := range map[string]struct {
		known   bool
		release Release
		want    model.Outcome
	}{
		"a better source beats the recorded one":    {true, Release{Facts: score.Facts{File: score.FileFacts{Source: "bluray"}}}, model.OutcomeAccept},
		"a repack beats the recorded original":      {true, Release{Facts: score.Facts{File: score.FileFacts{Source: "webdl"}}, Revised: true}, model.OutcomeAccept},
		"the same release again is a tie":           {true, Release{Facts: score.Facts{File: score.FileFacts{Source: "webdl"}}}, model.OutcomeReject},
		"a scanned file is not beaten by its claim": {false, Release{Facts: score.Facts{File: score.FileFacts{Source: "bluray"}}}, model.OutcomeReject},
	} {
		t.Run(name, func(t *testing.T) {
			incumbents, files := owned(test.known)
			yard := yardstick[struct{}]{rules: rules, incumbents: incumbents, ownedFiles: files, preferPropers: true}
			decision := model.Decision{Outcome: model.OutcomeAccept}
			yard.requireUpgrade(&decision, test.release)
			if decision.Outcome != test.want {
				t.Errorf("outcome = %v, want %v (%+v)", decision.Outcome, test.want, decision.Rejections)
			}
		})
	}
}
