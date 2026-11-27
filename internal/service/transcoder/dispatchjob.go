package transcoder

import (
	"context"
	"errors"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
)

// DispatchJobType keeps the analysis current and starts the next conversions.
const DispatchJobType = jobs.Type("transcode.dispatch")

// DispatchActionKey is the one dispatch that may be queued at a time.
const DispatchActionKey = "transcode.dispatch"

// DispatchDefinition declares the dispatcher: bookkeeping that submits the other jobs and settles a restart, so it is held back in observe mode.
var DispatchDefinition = jobs.Definition{Name: "Dispatching conversions", Type: DispatchJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2}

// EnqueueDispatch submits a dispatch; one already queued absorbs this one.
func EnqueueDispatch(ctx context.Context, queue Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: DispatchJobType, ActionKey: DispatchActionKey, Priority: jobs.PriorityLow})
}

// DispatchHandler adapts the dispatcher to the job queue.
type DispatchHandler struct{ Service *Service }

// Run dispatches once.
func (h DispatchHandler) Run(ctx context.Context, _ jobs.Job) error { return h.Service.Dispatch(ctx) }

// pickWindow is how many candidates of one library and kind the dispatcher looks at; most are skipped for a lane without room.
const pickWindow = 20

// Dispatch settles what a restart left, keeps every enabled library's analysis current and starts the next
// conversions the lanes have room for. The master switch off does nothing at all.
func (s *Service) Dispatch(ctx context.Context) error {
	global := s.Settings.Global()
	if s.SafeMode && global.Enabled && s.Logger != nil {
		s.Logger.Info("skipped: safe mode", "job", string(DispatchJobType))
	}
	if !global.Enabled || s.SafeMode {
		return nil
	}
	if err := s.reconcileOnce(ctx); err != nil {
		return err
	}
	all, err := s.Settings.Libraries(ctx)
	if err != nil {
		return err
	}
	var libraries []settings.TranscodeLibrary
	for _, one := range all {
		if one.Enabled && one.Supported() {
			libraries = append(libraries, one)
			if _, err := EnqueueAnalyze(ctx, s.Queue, one.CategoryID); err != nil {
				return err
			}
		}
	}
	state, err := s.dispatchState(ctx, libraries)
	if err != nil {
		return err
	}
	for _, pick := range PickNext(state) {
		if err := s.start(ctx, pick); err != nil {
			return err
		}
	}
	return nil
}

// reconcileOnce settles the restart's leftovers before the first conversion; a failure is tried again next time.
func (s *Service) reconcileOnce(ctx context.Context) error {
	s.mu.Lock()
	done := s.reconciled
	s.mu.Unlock()
	if done {
		return nil
	}
	if err := s.Reconcile(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.reconciled = true
	s.mu.Unlock()
	return nil
}

// dispatchState reads everything the choice of the next files depends on.
func (s *Service) dispatchState(ctx context.Context, libraries []settings.TranscodeLibrary) (DispatchState, error) {
	state := DispatchState{
		Enabled: true, Free: s.Slots.Free(), Health: map[string]Health{}, Running: map[string]int{}, LastRun: map[string]int64{}, BlockedRoots: s.rootsBlocked(),
	}
	now := s.Clock.Now()
	failedAt := map[string]int64{}
	for _, kind := range []string{KindVideo, KindAudio, KindComic, KindEbook} {
		stored, err := s.Items.Health(ctx, kind)
		if err != nil {
			return state, err
		}
		health := healthOf(stored)
		if health.Mode == ModeBackoff && !health.BackoffUntil.After(now) {
			health = AfterBackoff(health, now)
			if err := s.Items.SaveHealth(ctx, storedHealth(kind, health, stored.RunSeq, stored.LastError)); err != nil {
				return state, err
			}
		}
		state.Health[kind], state.LastRun[kind], failedAt[kind] = health, stored.RunSeq, health.LastFailedID
	}
	running, err := s.Items.Running(ctx)
	if err != nil {
		return state, err
	}
	for _, item := range running {
		state.Running[item.Kind]++
	}
	for _, one := range libraries {
		queue := LibraryQueue{
			ID: one.CategoryID, Priority: int(one.Priority), LastServed: s.lastServed(one.CategoryID),
			Candidates: map[string][]Candidate{}, FarAway: map[string][]Candidate{},
		}
		kind := one.Kind()
		picks, err := s.Items.Pick(ctx, library.TranscodeOrder(one.Order), one.CategoryID, kind, pickWindow)
		if err != nil {
			return state, err
		}
		queue.Candidates[kind] = candidatesOf(kind, picks)
		if state.Health[kind].Mode == ModeProbing {
			avoidRoot, avoidPrefix := s.whereFailed(ctx, failedAt[kind])
			far, err := s.Items.PickFarAway(ctx, one.CategoryID, kind, avoidRoot, avoidPrefix, pickWindow)
			if err != nil {
				return state, err
			}
			queue.FarAway[kind] = candidatesOf(kind, far)
		}
		state.Libraries = append(state.Libraries, queue)
	}
	return state, nil
}

// candidatesOf names each pick's lane: a comic is the comic lane's, whatever the item row stores.
func candidatesOf(kind string, picks []library.TranscodePick) []Candidate {
	out := make([]Candidate, len(picks))
	for i, pick := range picks {
		lane := Lane(pick.Lane)
		if kind == KindComic || kind == KindEbook {
			lane = LaneBooks
		}
		out[i] = Candidate{MediaObjectID: pick.MediaObjectID, Lane: lane, LastRunSeq: pick.LastRunSeq, RootFolderID: pick.RootFolderID, Interrupted: pick.Interrupted}
	}
	return out
}

// whereFailed is the root and top-level folder of the file that failed last, which a probe stays away from.
func (s *Service) whereFailed(ctx context.Context, id int64) (rootID int64, prefix string) {
	if id == 0 {
		return 0, ""
	}
	paths, err := s.Items.Paths(ctx, id)
	if err != nil || len(paths) == 0 {
		return 0, ""
	}
	first, _, nested := strings.Cut(paths[0].RelPath, "/")
	if !nested {
		return paths[0].RootFolderID, ""
	}
	return paths[0].RootFolderID, first + "/"
}

func (s *Service) lastServed(categoryID int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served[categoryID]
}

// start marks the picked file running under a new run number and queues its conversion.
func (s *Service) start(ctx context.Context, pick Pick) error {
	stored, err := s.Items.Health(ctx, pick.Kind)
	if err != nil {
		return err
	}
	stored.RunSeq++
	if err := s.Items.SaveHealth(ctx, stored); err != nil {
		return err
	}
	id := pick.Candidate.MediaObjectID
	err = s.Items.Transition(ctx, id, library.TranscodeNeeded, library.TranscodeRunning, func(item *library.TranscodeItem) {
		item.LastRunSeq, item.LastError, item.FailureCode = stored.RunSeq, "", ""
	})
	if errors.Is(err, library.ErrTranscodeState) || errors.Is(err, library.ErrNoTranscodeItem) {
		return nil
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.served == nil {
		s.served = map[int64]int64{}
	}
	s.serveSeq++
	s.served[pick.LibraryID] = s.serveSeq
	s.mu.Unlock()
	if _, err := EnqueueFile(ctx, s.Queue, id, pick.LibraryID); err != nil {
		_ = s.Items.Transition(ctx, id, library.TranscodeRunning, library.TranscodeNeeded, nil)
		return err
	}
	return nil
}
