package tv

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestAnUpgradePackMostlyOfGoodEpisodesIsRejected(t *testing.T) {
	yard := yardstick{rules: []judge.RankedProfile{{Name: "any"}}, incumbents: map[int]model.Decision{1: {}}}
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
		yard.requireUpgrade(&decision, score.Facts{}, test.numbers, test.held)
		packRejected := decision.Outcome == model.OutcomeReject && len(decision.Rejections) > 0 && decision.Rejections[0].Field == "pack"
		if packRejected != test.rejected {
			t.Errorf("covering %v of %d episodes: rejected for its size = %v, want %v", test.numbers, test.held, packRejected, test.rejected)
		}
	}
}
