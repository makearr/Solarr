package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/paginate"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/jobsdb"
)

var (
	// ErrUnknownType reports a job type that was never registered.
	ErrUnknownType = errors.New("jobs: unknown type")

	// ErrNotFound reports a job id that is not in the queue.
	ErrNotFound = errors.New("jobs: not found")

	// ErrLeaseLost reports an operation by a worker that no longer holds the
	// lease, because the reaper took it back. The worker must stop rather than
	// race whoever has it now.
	ErrLeaseLost = errors.New("jobs: lease no longer held")
)

// DefaultLeaseTTL is how long a claim lasts without renewal. Long enough to
// survive a slow step, short enough that a crashed process's work is picked up
// promptly (architecture section 7.2).
const DefaultLeaseTTL = 60 * time.Second

// Queue is the durable job queue over jobs.db.
type Queue struct {
	store    *store.Store
	queries  *jobsdb.Queries
	clock    clock.Clock
	registry *Registry
	// jitter spreads backoff. Injected so retry timing is deterministic in
	// tests (ADR-17).
	jitter func(time.Duration) time.Duration
}

// NewQueue builds a queue. A nil jitter takes full jitter.
func NewQueue(jobStore *store.Store, cl clock.Clock, registry *Registry,
	jitter func(time.Duration) time.Duration) *Queue {
	if jitter == nil {
		jitter = fullJitter
	}
	return &Queue{
		store:    jobStore,
		queries:  jobsdb.New(jobStore.Read()),
		clock:    cl,
		registry: registry,
		jitter:   jitter,
	}
}

// EnqueueResult reports what happened. Duplicate means an equivalent job was
// already queued and this one was not created — ADR-6's deduplication, which is
// what stops a restart re-downloading everything in flight.
type EnqueueResult struct {
	Job       Job
	Duplicate bool
}

// Enqueue adds a job, or returns the active one already carrying its action key.
//
// The existence check and the insert share one transaction. Because there is
// exactly one writer per database (ADR-3), that is enough to make the pair
// atomic without depending on the driver's phrasing of a unique-constraint
// violation.
func (q *Queue) Enqueue(ctx context.Context, request Request) (EnqueueResult, error) {
	definition, known := q.registry.Lookup(request.Type)
	if !known {
		return EnqueueResult{}, fmt.Errorf("%w: %s", ErrUnknownType, request.Type)
	}

	now := q.clock.Now()
	runAfter := request.RunAfter
	if runAfter.IsZero() {
		runAfter = now
	}
	priority := request.Priority
	if priority == 0 {
		priority = PriorityNormal
	}

	var result EnqueueResult
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := q.queries.WithTx(tx)

		if request.ActionKey != "" {
			existing, err := queries.FindActiveJobByActionKey(ctx, sql.NullString{
				String: request.ActionKey, Valid: true,
			})
			if err == nil {
				result = EnqueueResult{Job: convertJob(existing), Duplicate: true}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("look up action key: %w", err)
			}
		}

		created, err := queries.EnqueueJob(ctx, jobsdb.EnqueueJobParams{
			Type:          string(request.Type),
			Pool:          string(definition.Pool),
			ActionKey:     nullString(request.ActionKey),
			Priority:      int64(priority),
			PayloadJson:   string(request.Payload),
			State:         string(StatePending),
			Mutating:      boolToInt(definition.Mutating),
			MaxAttempts:   int64(definition.MaxAttempts),
			RunAfter:      runAfter.Unix(),
			DependsOn:     nullInt(request.DependsOn),
			CorrelationID: nullString(request.CorrelationID),
			UserID:        nullInt(request.UserID),
			CreatedAt:     now.Unix(),
			UpdatedAt:     now.Unix(),
			CategoryID:    nullInt(request.CategoryID),
		})
		if err != nil {
			return fmt.Errorf("insert: %w", err)
		}
		result = EnqueueResult{Job: convertJob(created)}
		return nil
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("jobs: enqueue %s: %w", request.Type, err)
	}
	return result, nil
}

// Get reads one job.
func (q *Queue) Get(ctx context.Context, id int64) (Job, error) {
	row, err := q.queries.GetJob(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("%w: job %d", ErrNotFound, id)
	}
	if err != nil {
		return Job{}, fmt.Errorf("jobs: get %d: %w", id, err)
	}
	return convertJob(row), nil
}

// Claim leases up to capacity jobs for one pool.
//
// It is a single conditional UPDATE ... RETURNING, so two dispatchers cannot
// hand the same job to two workers however they interleave.
func (q *Queue) Claim(ctx context.Context, pool Pool, owner string, capacity int,
	leaseTTL time.Duration) ([]Job, error) {
	if capacity <= 0 {
		return nil, nil
	}
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}

	now := q.clock.Now()
	var claimed []jobsdb.Job
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := q.queries.WithTx(tx).ClaimJobs(ctx, jobsdb.ClaimJobsParams{
			LeaseOwner:     nullString(owner),
			LeaseExpiresAt: sql.NullInt64{Int64: now.Add(leaseTTL).Unix(), Valid: true},
			Now:            now.Unix(),
			TargetPool:     string(pool),
			Capacity:       int64(capacity),
		})
		claimed = rows
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: claim for %s: %w", pool, err)
	}

	jobs := make([]Job, 0, len(claimed))
	for _, row := range claimed {
		jobs = append(jobs, convertJob(row))
	}

	// The subquery's ORDER BY decides which jobs are claimed; it does not decide
	// the order RETURNING yields them in, which SQLite leaves unspecified and
	// which came back in rowid order. Callers hand these to workers in sequence,
	// so the order is sorted here rather than left to the storage engine.
	sort.SliceStable(jobs, func(i, j int) bool {
		if jobs[i].Priority != jobs[j].Priority {
			return jobs[i].Priority > jobs[j].Priority
		}
		return jobs[i].RunAfter.Before(jobs[j].RunAfter)
	})
	return jobs, nil
}

// Start moves a claimed job to running and records the attempt.
func (q *Queue) Start(ctx context.Context, job Job, owner string) (Attempt, error) {
	now := q.clock.Now()
	var attempt jobsdb.JobAttempt

	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := q.queries.WithTx(tx)
		if err := queries.StartJob(ctx, jobsdb.StartJobParams{
			UpdatedAt:  now.Unix(),
			ID:         job.ID,
			LeaseOwner: nullString(owner),
		}); err != nil {
			return err
		}
		recorded, err := queries.RecordAttemptStart(ctx, jobsdb.RecordAttemptStartParams{
			JobID:      job.ID,
			Attempt:    int64(job.Attempts + 1),
			LeaseOwner: owner,
			StartedAt:  now.Unix(),
		})
		attempt = recorded
		return err
	})
	if err != nil {
		return Attempt{}, fmt.Errorf("jobs: start %d: %w", job.ID, err)
	}
	return convertAttempt(attempt), nil
}

// Renew extends a lease. It fails with ErrLeaseLost if the reaper already took
// the job back, which is the worker's signal to stop rather than race the
// worker that has it now.
func (q *Queue) Renew(ctx context.Context, job Job, owner string, leaseTTL time.Duration) error {
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}
	now := q.clock.Now()

	var affected int64
	err := q.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := q.queries.WithTx(tx).RenewLease(ctx, jobsdb.RenewLeaseParams{
			LeaseExpiresAt: sql.NullInt64{Int64: now.Add(leaseTTL).Unix(), Valid: true},
			UpdatedAt:      now.Unix(),
			ID:             job.ID,
			LeaseOwner:     nullString(owner),
		})
		affected = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("jobs: renew %d: %w", job.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: job %d", ErrLeaseLost, job.ID)
	}
	return nil
}

// Visibility narrows a job listing to what a caller may see (ADR-27).
//
// It is a required argument rather than an option with a permissive default: a
// caller that forgets it would see every job, and the whole point of applying
// the filter in the repository is that it cannot be forgotten (Invariant 36).
type Visibility struct {
	// Categories the caller may read. A job scoped to any other category is
	// invisible.
	Categories []int64
	// System admits jobs belonging to no category -- backups, database
	// maintenance. Whether those are visible is a question about roles, not
	// about categories, so the caller decides.
	System bool
}

// ListPage returns one page of jobs the caller may see, newest first.
//
// state may be empty to list every state. Paging is by cursor rather than
// offset, so the thousandth page costs the same as the first (Invariant 32).
func (q *Queue) ListPage(ctx context.Context, visibility Visibility, state State,
	after paginate.Cursor, limit int) (paginate.Page[Job], error) {
	limit = paginate.ClampLimit(limit)

	categories, err := json.Marshal(visibility.Categories)
	if err != nil {
		return paginate.Page[Job]{}, fmt.Errorf("jobs: encode visible categories: %w", err)
	}

	// One more row than asked for, so the last page is recognisable without a
	// second counting query.
	rows, err := q.queries.ListJobsPage(ctx, jobsdb.ListJobsPageParams{
		State:             string(state),
		IncludeSystem:     boolToInt(visibility.System),
		VisibleCategories: string(categories),
		AfterSort:         after.Sort,
		AfterID:           after.ID,
		PageSize:          int64(limit + 1),
	})
	if err != nil {
		return paginate.Page[Job]{}, fmt.Errorf("jobs: list page: %w", err)
	}

	jobs := make([]Job, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, convertJob(row))
	}
	return paginate.Build(jobs, limit, func(job Job) paginate.Cursor {
		return paginate.Cursor{Sort: job.UpdatedAt.Unix(), ID: job.ID}
	}), nil
}
