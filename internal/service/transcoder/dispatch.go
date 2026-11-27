package transcoder

import "sort"

// The two kinds of conversion health is kept for.
const (
	KindVideo = "video"
	KindAudio = "audio"
)

// Candidate is a file waiting for a conversion, as its library's list order offers it.
type Candidate struct {
	MediaObjectID int64
	Lane          Lane
	LastRunSeq    int64
	RootFolderID  int64
	Interrupted   bool
}

// LibraryQueue is one enabled library's candidates; Candidates follow its list order, FarAway is the far end for a probe.
type LibraryQueue struct {
	ID         int64
	Priority   int
	LastServed int64
	Candidates map[string][]Candidate
	FarAway    map[string][]Candidate
}

// DispatchState is everything the choice of the next files depends on.
type DispatchState struct {
	Enabled   bool
	Libraries []LibraryQueue
	// Free is how many more files each lane may start.
	Free map[Lane]int
	// Health and Running are per kind; Running counts files of that kind already converting.
	Health  map[string]Health
	Running map[string]int
	// LastRun is the run sequence of the file started last, per kind.
	LastRun      map[string]int64
	BlockedRoots map[int64]bool
}

// Pick is one file to start.
type Pick struct {
	LibraryID int64
	Kind      string
	Candidate Candidate
}

// PickNext chooses the files to start now: libraries by priority then least recently served, round robin within a
// priority, interrupted files first as the queues already order them, never the file that ran last while another waits,
// far away after failures, nothing of a kind in backoff, and nothing on a root without space.
func PickNext(state DispatchState) []Pick {
	if !state.Enabled {
		return nil
	}
	libraries := append([]LibraryQueue(nil), state.Libraries...)
	sort.SliceStable(libraries, func(i, j int) bool {
		a, b := libraries[i], libraries[j]
		switch {
		case a.Priority != b.Priority:
			return a.Priority > b.Priority
		case a.LastServed != b.LastServed:
			return a.LastServed < b.LastServed
		}
		return a.ID < b.ID
	})
	free := map[Lane]int{}
	for lane, count := range state.Free {
		free[lane] = count
	}
	taken := map[int64]bool{}
	started := map[string]int{}
	var picks []Pick
	for start := 0; start < len(libraries); {
		end := start
		for end < len(libraries) && libraries[end].Priority == libraries[start].Priority {
			end++
		}
		group := libraries[start:end]
		for progressed := true; progressed; {
			progressed = false
			for _, library := range group {
				if pick, ok := pickFrom(state, library, free, taken, started); ok {
					free[pick.Candidate.Lane]--
					taken[pick.Candidate.MediaObjectID] = true
					started[pick.Kind]++
					picks = append(picks, pick)
					progressed = true
				}
			}
		}
		start = end
	}
	return picks
}

// pickFrom takes the first usable candidate of the library, video before audio.
func pickFrom(state DispatchState, library LibraryQueue, free map[Lane]int, taken map[int64]bool, started map[string]int) (Pick, bool) {
	for _, kind := range []string{KindVideo, KindAudio, KindComic, KindEbook} {
		health := state.Health[kind]
		source := library.Candidates[kind]
		switch health.Mode {
		case ModeBackoff:
			continue
		case ModeProbing:
			if state.Running[kind]+started[kind] > 0 {
				continue
			}
			source = library.FarAway[kind]
		}
		if candidate, ok := firstUsable(source, state, kind, free, taken); ok {
			return Pick{LibraryID: library.ID, Kind: kind, Candidate: candidate}, true
		}
	}
	return Pick{}, false
}

// firstUsable skips files in a lane without a free slot, on a blocked root, already picked, and the one that ran last
// unless it is the only candidate left.
func firstUsable(candidates []Candidate, state DispatchState, kind string, free map[Lane]int, taken map[int64]bool) (Candidate, bool) {
	var heldBack *Candidate
	for i, candidate := range candidates {
		if taken[candidate.MediaObjectID] || free[candidate.Lane] <= 0 || state.BlockedRoots[candidate.RootFolderID] {
			continue
		}
		if last := state.LastRun[kind]; last != 0 && candidate.LastRunSeq == last {
			if heldBack == nil {
				heldBack = &candidates[i]
			}
			continue
		}
		return candidate, true
	}
	if heldBack != nil {
		return *heldBack, true
	}
	return Candidate{}, false
}
