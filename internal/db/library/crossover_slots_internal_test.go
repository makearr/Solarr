package library

import (
	"reflect"
	"testing"
)

func episode(partID int64, number int, date string) eventEpisode {
	return eventEpisode{partID: partID, workID: partID, number: number, date: date, at: parseDate(date)}
}

type placement struct {
	partID  int64
	slot    int
	guessed bool
}

func placements(slots []slottedEpisode) []placement {
	out := make([]placement, len(slots))
	for i, slot := range slots {
		out[i] = placement{slot.partID, slot.slot, slot.guessed}
	}
	return out
}

func TestSlotEpisodesPutsAnUnnumberedEpisodeIntoTheGapByAirDate(t *testing.T) {
	slots, total := slotEpisodes([]eventEpisode{episode(1, 1, "2019-01-14"), episode(2, 0, "2019-01-15"), episode(3, 3, "2019-01-16")}, 0)
	want := []placement{{1, 1, false}, {2, 2, true}, {3, 3, false}}
	if got := placements(slots); total != 3 || !reflect.DeepEqual(got, want) {
		t.Errorf("slots = %+v, total %d; want %+v, 3", got, total, want)
	}
}

func TestSlotEpisodesKnowsTheTotalAndLeavesTheGapsOfAnEventWithOnePartNumbered(t *testing.T) {
	slots, total := slotEpisodes([]eventEpisode{episode(1, 0, "2016-11-28"), episode(2, 4, "2016-12-01")}, 4)
	want := []placement{{1, 3, true}, {2, 4, false}}
	if got := placements(slots); total != 4 || !reflect.DeepEqual(got, want) {
		t.Errorf("slots = %+v, total %d; want %+v, 4 (the unnumbered one aired first, so the free slot nearest part 4)", got, total, want)
	}
}

func TestSlotEpisodesTreatsTwoEpisodesWithOneNumberAsOneNumberedAndOneGuess(t *testing.T) {
	slots, total := slotEpisodes([]eventEpisode{episode(1, 2, "2019-01-14"), episode(2, 2, "2019-01-15")}, 0)
	want := []placement{{2, 1, true}, {1, 2, false}}
	if got := placements(slots); total != 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("slots = %+v, total %d; want %+v, 2", got, total, want)
	}
}
