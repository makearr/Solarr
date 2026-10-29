package importlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
)

// JobType is the sync of the import lists that are due, or of one list.
const JobType = jobs.Type("importlist.sync")

// actionKey deduplicates the sweep, and with a list id one list's own run (Invariant 16).
const actionKey = "importlist.sync"

// Definition declares the sync: background metadata work that adds works, retried on transient failure.
var Definition = jobs.Definition{
	Name: "Adding what import lists name", Type: JobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// Payload scopes a run to one list, run now whether due or not; empty is the sweep of the lists that are due.
type Payload struct {
	ListID int64 `json:"list_id,omitempty"`
}

// Failure names why a list's run stopped, for the person to act on; the web interface words it.
type Failure string

// The failures a run records; FailureNone is a run that finished.
const (
	FailureNone              Failure = ""
	FailureNeedsKey          Failure = "needs_key"
	FailureListNotFound      Failure = "list_not_found"
	FailureSourceUnavailable Failure = "source_unavailable"
	FailureNotSupported      Failure = "not_supported"
	FailureOther             Failure = "failed"
)

// FailureOf classifies a pass's error.
func FailureOf(err error) Failure {
	switch {
	case err == nil:
		return FailureNone
	case errors.Is(err, ErrNeedsKey):
		return FailureNeedsKey
	case errors.Is(err, ErrListNotFound):
		return FailureListNotFound
	case errors.Is(err, ErrSourceUnavailable), errors.Is(err, media.ErrSourceUnavailable):
		return FailureSourceUnavailable
	case errors.Is(err, ErrNotSupported), errors.Is(err, media.ErrNotWired):
		return FailureNotSupported
	}
	return FailureOther
}

// Enqueue submits the sweep, or one list's own run when listID is set.
func Enqueue(ctx context.Context, queue *jobs.Queue, listID int64) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(Payload{ListID: listID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("importlist: payload: %w", err)
	}
	key, priority := actionKey, jobs.PriorityLow
	if listID != 0 {
		key, priority = actionKey+":"+strconv.FormatInt(listID, 10), jobs.PriorityNormal
	}
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: key, Priority: priority, Payload: payload})
}

// Run is the job: every enabled list that is due, or the one the payload names; one list failing never stops the rest.
func (s Service) Run(ctx context.Context, job jobs.Job) error {
	var payload Payload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Terminalf("importlist.payload", string(JobType), err)
		}
	}
	lists, err := s.Lists.EnabledImportLists(ctx)
	if err != nil {
		return fmt.Errorf("importlist: sweep: %w", err)
	}
	now := s.Clock.Now()
	added, failed := 0, 0
	for _, list := range lists {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("importlist.context", string(JobType), err)
		}
		if payload.ListID != 0 && list.ID != payload.ListID {
			continue
		}
		if payload.ListID == 0 {
			due, err := s.due(ctx, list.ID, list.CadenceHours, now)
			if err != nil {
				return err
			}
			if !due {
				continue
			}
		}
		count, err := s.sync(ctx, list.ID)
		added += count
		if err != nil {
			failed++
			s.logger().WarnContext(ctx, "import list failed", slog.Int64("list_id", list.ID), slog.Any("error", err))
		}
	}
	if added > 0 && s.AfterAdd != nil {
		s.AfterAdd(ctx)
	}
	s.logger().Info("import list sync finished", slog.Int("added", added), slog.Int("failed", failed))
	return nil
}

// due reports a list whose cadence has passed since its last run.
func (s Service) due(ctx context.Context, listID, cadenceHours int64, now time.Time) (bool, error) {
	state, err := s.Records.State(ctx, listID)
	if err != nil {
		return false, err
	}
	return state.RunAt == 0 || now.Unix()-state.RunAt >= cadenceHours*int64(time.Hour/time.Second), nil
}

// sync runs one list: reads it, adds up to runAdds of what is new, keeps what it made of each entry, records the run.
func (s Service) sync(ctx context.Context, listID int64) (int, error) {
	added, err := s.syncEntries(ctx, listID)
	if recordErr := s.Records.RecordRun(ctx, listID, s.Clock.Now(), added, string(FailureOf(err))); recordErr != nil {
		return added, errors.Join(err, recordErr)
	}
	return added, err
}

func (s Service) syncEntries(ctx context.Context, listID int64) (int, error) {
	list, err := s.Lists.ImportList(ctx, listID)
	if err != nil {
		return 0, err
	}
	aimed, err := s.target(list)
	if err != nil {
		return 0, err
	}
	adding, ok := media.Capability[media.Adding](s.Media, media.Key(aimed.category.MediaType))
	if !ok {
		return 0, ErrNotSupported
	}
	judged, err := s.judge(ctx, aimed, runResolutions)
	if err != nil {
		return 0, err
	}
	now := s.Clock.Now().Unix()
	record := func(verdict Judged, outcome library.ListOutcome, workID int64) library.ImportListEntry {
		key := verdict.Entry.Key()
		return library.ImportListEntry{
			ListID: list.ID, Provider: key.Provider, ExternalID: key.ExternalID, Outcome: outcome, WorkID: workID,
			Title: verdict.Entry.Title, SeenAt: now,
		}
	}
	var kept []library.ImportListEntry
	added := 0
	for _, verdict := range judged {
		switch verdict.Verdict {
		case VerdictHeld:
			kept = append(kept, record(verdict, library.ListHeld, verdict.WorkID))
		case VerdictForeign:
			kept = append(kept, record(verdict, library.ListForeign, 0))
		case VerdictUnknown:
			kept = append(kept, record(verdict, library.ListUnresolved, 0))
		case VerdictAdd:
			if added == runAdds {
				continue
			}
			result, err := s.add(ctx, aimed, adding, verdict.Match)
			if errors.Is(err, media.ErrInvalidAdd) {
				s.logger().WarnContext(ctx, "import list entry not added", slog.Int64("list_id", list.ID),
					slog.String("title", verdict.Entry.Title), slog.Any("error", err))
				kept = append(kept, record(verdict, library.ListUnresolved, 0))
				continue
			}
			if err != nil {
				return added, errors.Join(err, s.Records.Record(ctx, kept))
			}
			added++
			kept = append(kept, record(verdict, library.ListAdded, result.WorkID))
		}
	}
	return added, s.Records.Record(ctx, kept)
}

// add adds one entry through the media type's own add, with the list's monitoring and profile.
func (s Service) add(ctx context.Context, aimed target, adding media.Adding, match media.ProviderID) (media.AddResult, error) {
	request := media.AddRequest{
		CategoryID: aimed.category.ID, Provider: match.Provider, ExternalID: match.ExternalID,
		Monitored: aimed.list.Monitor, Option: aimed.list.MonitorOption, ProfileID: aimed.list.QualityProfileID,
	}
	if module, ok := s.Media.Module(media.Key(aimed.category.MediaType)); ok {
		if kinds := module.Descriptor().AddKinds; len(kinds) > 0 {
			request.Kind = kinds[0].Key
		}
	}
	return adding.AddWork(ctx, request)
}
