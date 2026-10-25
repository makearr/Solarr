package works

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
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
