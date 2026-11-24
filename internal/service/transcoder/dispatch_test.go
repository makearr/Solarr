package transcoder_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/service/transcoder"
)

func cand(id int64, lane transcoder.Lane) transcoder.Candidate {
	return transcoder.Candidate{MediaObjectID: id, Lane: lane, RootFolderID: 1}
}

func libraryOf(id int64, priority int, served int64, videos ...transcoder.Candidate) transcoder.LibraryQueue {
	return transcoder.LibraryQueue{ID: id, Priority: priority, LastServed: served, Candidates: map[string][]transcoder.Candidate{transcoder.KindVideo: videos}}
}

func stateOf(libraries ...transcoder.LibraryQueue) transcoder.DispatchState {
	return transcoder.DispatchState{
		Enabled: true, Libraries: libraries, Free: map[transcoder.Lane]int{transcoder.LaneCPU: 2, transcoder.LaneGPU: 1, transcoder.LaneAudio: 4},
		Health: map[string]transcoder.Health{}, Running: map[string]int{}, LastRun: map[string]int64{}, BlockedRoots: map[int64]bool{},
	}
}

func pickedIDs(picks []transcoder.Pick) []int64 {
	var ids []int64
	for _, pick := range picks {
		ids = append(ids, pick.Candidate.MediaObjectID)
	}
	return ids
}

func TestPickNext_TheMasterSwitchOffPicksNothing(t *testing.T) {
	state := stateOf(libraryOf(1, 0, 0, cand(10, transcoder.LaneCPU)))
	state.Enabled = false
	if picks := transcoder.PickNext(state); len(picks) != 0 {
		t.Errorf("picks = %+v", picks)
	}
}

func TestPickNext_HigherPriorityFirstThenLeastRecentlyServedAndLanesFillToTheirFreeSlots(t *testing.T) {
	state := stateOf(
		libraryOf(1, 0, 5, cand(10, transcoder.LaneCPU), cand(11, transcoder.LaneCPU)),
		libraryOf(2, 5, 9, cand(20, transcoder.LaneCPU), cand(21, transcoder.LaneCPU), cand(22, transcoder.LaneCPU)),
	)
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{20, 21}) {
		t.Errorf("picks = %v, want the high priority library to take both cpu slots", got)
	}
	state = stateOf(libraryOf(1, 0, 5, cand(10, transcoder.LaneCPU), cand(11, transcoder.LaneCPU)), libraryOf(2, 0, 2, cand(20, transcoder.LaneCPU), cand(21, transcoder.LaneCPU)))
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{20, 10}) {
		t.Errorf("picks = %v, want round robin starting with the least recently served", got)
	}
	state = stateOf(libraryOf(1, 0, 0, cand(10, transcoder.LaneCPU), cand(11, transcoder.LaneGPU), cand(12, transcoder.LaneGPU), cand(13, transcoder.LaneCPU), cand(14, transcoder.LaneCPU)))
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{10, 11, 13}) {
		t.Errorf("picks = %v, want two cpu and one gpu", got)
	}
}

func TestPickNext_TheFileThatRanLastIsSkippedWhileAnotherWaitsButTakenWhenAlone(t *testing.T) {
	last := cand(10, transcoder.LaneCPU)
	last.LastRunSeq = 7
	state := stateOf(libraryOf(1, 0, 0, last, cand(11, transcoder.LaneCPU)))
	state.Free = map[transcoder.Lane]int{transcoder.LaneCPU: 1}
	state.LastRun[transcoder.KindVideo] = 7
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{11}) {
		t.Errorf("picks = %v, want the other file", got)
	}
	state = stateOf(libraryOf(1, 0, 0, last))
	state.Free = map[transcoder.Lane]int{transcoder.LaneCPU: 1}
	state.LastRun[transcoder.KindVideo] = 7
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{10}) {
		t.Errorf("picks = %v, want the lone file despite having run last", got)
	}
}

func TestPickNext_BackoffHoldsAKindAndProbingTakesOneFarFileAtATime(t *testing.T) {
	library := libraryOf(1, 0, 0, cand(10, transcoder.LaneCPU), cand(11, transcoder.LaneCPU))
	library.FarAway = map[string][]transcoder.Candidate{transcoder.KindVideo: {cand(99, transcoder.LaneCPU), cand(98, transcoder.LaneCPU)}}
	state := stateOf(library)
	state.Health[transcoder.KindVideo] = transcoder.Health{Mode: transcoder.ModeBackoff}
	if picks := transcoder.PickNext(state); len(picks) != 0 {
		t.Errorf("backoff picked %+v", picks)
	}
	state.Health[transcoder.KindVideo] = transcoder.Health{Mode: transcoder.ModeProbing, ProbesLeft: 3}
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{99}) {
		t.Errorf("probing picked %v, want one far file", got)
	}
	state.Running[transcoder.KindVideo] = 1
	if picks := transcoder.PickNext(state); len(picks) != 0 {
		t.Errorf("a probe already running, picked %+v", picks)
	}
}

func TestPickNext_ARootShortOfSpaceIsSkippedAndAudioHasItsOwnLane(t *testing.T) {
	blocked := transcoder.Candidate{MediaObjectID: 10, Lane: transcoder.LaneCPU, RootFolderID: 5}
	library := libraryOf(1, 0, 0, blocked, cand(11, transcoder.LaneCPU))
	library.Candidates[transcoder.KindAudio] = []transcoder.Candidate{cand(30, transcoder.LaneAudio)}
	state := stateOf(library)
	state.BlockedRoots[5] = true
	state.Free = map[transcoder.Lane]int{transcoder.LaneCPU: 1, transcoder.LaneAudio: 1}
	if got := pickedIDs(transcoder.PickNext(state)); !reflect.DeepEqual(got, []int64{11, 30}) {
		t.Errorf("picks = %v", got)
	}
}
