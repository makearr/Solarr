package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

const (
	typeBackup = jobs.Type("db.backup")
	typeImport = jobs.Type("import.run")
)

func newQueue(t *testing.T) (*jobs.Queue, *clock.Fake) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "jobs", Path: filepath.Join(directory, "jobs.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Jobs()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	registry := jobs.NewRegistry()
	registry.MustRegister(jobs.Definition{Type: typeBackup, Pool: jobs.PoolMaintenance})
	registry.MustRegister(jobs.Definition{
		Type: typeImport, Pool: jobs.PoolImport, Mutating: true, MaxAttempts: 3,
	})

	return jobs.NewQueue(opened, fake, registry, jobs.NoJitter), fake
}

func mustEnqueue(t *testing.T, queue *jobs.Queue, request jobs.Request) jobs.Job {
	t.Helper()
	result, err := queue.Enqueue(t.Context(), request)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return result.Job
}

func TestEnqueue_StoresTheDefinitionsPoolAndMutability(t *testing.T) {
	queue, _ := newQueue(t)

	// The definition decides, not the caller, so two callers cannot disagree
	// about whether a type mutates.
	backup := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	if backup.Pool != jobs.PoolMaintenance || backup.Mutating {
		t.Errorf("db.backup = pool %q mutating %v, want maintenance and false", backup.Pool, backup.Mutating)
	}

	imported := mustEnqueue(t, queue, jobs.Request{Type: typeImport})
	if imported.Pool != jobs.PoolImport || !imported.Mutating {
		t.Errorf("import.run = pool %q mutating %v, want import and true", imported.Pool, imported.Mutating)
	}
	if imported.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want the definition's 3", imported.MaxAttempts)
	}
}

func TestEnqueue_RejectsAnUnregisteredType(t *testing.T) {
	queue, _ := newQueue(t)
	if _, err := queue.Enqueue(t.Context(), jobs.Request{Type: "never.registered"}); !errors.Is(err, jobs.ErrUnknownType) {
		t.Errorf("Enqueue = %v, want ErrUnknownType", err)
	}
}

// Invariant 16: every planned action carries a deterministic action_key and the
// queue deduplicates on it, so a restart cannot enqueue the same download twice.
func TestEnqueue_DeduplicatesOnActionKey(t *testing.T) {
	queue, _ := newQueue(t)

	first, err := queue.Enqueue(t.Context(), jobs.Request{Type: typeImport, ActionKey: "acquire:42"})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if first.Duplicate {
		t.Error("the first enqueue reported a duplicate")
	}

	second, err := queue.Enqueue(t.Context(), jobs.Request{Type: typeImport, ActionKey: "acquire:42"})
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("the second enqueue created a new job; the action key did not deduplicate")
	}
	if second.Job.ID != first.Job.ID {
		t.Errorf("returned job %d, want the existing %d", second.Job.ID, first.Job.ID)
	}
}

func TestEnqueue_ActionKeyIsFreedOnceTheJobFinishes(t *testing.T) {
	queue, _ := newQueue(t)

	first := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, ActionKey: "backup:nightly"})
	claimed := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	attempt := startJob(t, queue, claimed, "worker-1")
	if err := queue.Complete(t.Context(), claimed, "worker-1", attempt); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// A completed job must not block the next one, or a nightly backup runs once.
	second, err := queue.Enqueue(t.Context(), jobs.Request{Type: typeBackup, ActionKey: "backup:nightly"})
	if err != nil {
		t.Fatalf("Enqueue after completion: %v", err)
	}
	if second.Duplicate || second.Job.ID == first.ID {
		t.Error("a finished job still blocks its action key")
	}
}

func TestEnqueue_EmptyActionKeyDoesNotDeduplicate(t *testing.T) {
	queue, _ := newQueue(t)

	first := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	second := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	if first.ID == second.ID {
		t.Error("two keyless jobs collapsed into one")
	}
}

func claimOne(t *testing.T, queue *jobs.Queue, pool jobs.Pool, owner string) jobs.Job {
	t.Helper()
	return claimOneWithTTL(t, queue, pool, owner, time.Minute)
}

func claimOneWithTTL(t *testing.T, queue *jobs.Queue, pool jobs.Pool, owner string,
	leaseTTL time.Duration) jobs.Job {
	t.Helper()
	claimed, err := queue.Claim(t.Context(), pool, owner, 1, leaseTTL)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(claimed))
	}
	return claimed[0]
}

func startJob(t *testing.T, queue *jobs.Queue, job jobs.Job, owner string) jobs.Attempt {
	t.Helper()
	attempt, err := queue.Start(t.Context(), job, owner)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return attempt
}

func TestClaim_HandsAJobToExactlyOneWorker(t *testing.T) {
	queue, _ := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	first, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	second, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-2", 5, time.Minute)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}

	if len(first) != 1 || len(second) != 0 {
		t.Errorf("claims were %d and %d, want 1 and 0; the same job went to two workers",
			len(first), len(second))
	}
}

func TestClaim_OnlyTakesFromTheRequestedPool(t *testing.T) {
	queue, _ := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("the maintenance pool claimed %d import jobs", len(claimed))
	}
}

// Scarce capacity must go to the most important work, not to whichever row
// SQLite reaches first.
func TestClaim_TakesTheHighestPriorityWhenCapacityIsScarce(t *testing.T) {
	queue, fake := newQueue(t)

	fake.Advance(time.Hour)
	low := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityLow})
	fake.Advance(time.Minute)
	high := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityInteractive})

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 1, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d, want 1", len(claimed))
	}
	if claimed[0].ID != high.ID {
		t.Errorf("claimed job %d, want the interactive one %d (the older low-priority job is %d)",
			claimed[0].ID, high.ID, low.ID)
	}
}

// RETURNING yields rows in update order, which SQLite leaves unspecified and
// which came back as rowid order. Workers take these in sequence, so the order
// is not left to the storage engine.
func TestClaim_ReturnsHighestPriorityFirst(t *testing.T) {
	queue, fake := newQueue(t)

	fake.Advance(time.Hour)
	low := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityLow})
	fake.Advance(time.Minute)
	high := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityInteractive})

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 2, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d, want 2", len(claimed))
	}
	if claimed[0].ID != high.ID || claimed[1].ID != low.ID {
		t.Errorf("claim order was %d,%d; want %d (interactive) then %d (low)",
			claimed[0].ID, claimed[1].ID, high.ID, low.ID)
	}
}

func TestClaim_OldestFirstWithinAPriority(t *testing.T) {
	queue, fake := newQueue(t)

	fake.Advance(time.Hour)
	older := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityNormal})
	fake.Advance(time.Minute)
	newer := mustEnqueue(t, queue, jobs.Request{Type: typeBackup, Priority: jobs.PriorityNormal})

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 1, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != older.ID {
		t.Errorf("claimed %+v, want the older job %d rather than %d", claimed, older.ID, newer.ID)
	}
}

func TestClaim_RespectsRunAfter(t *testing.T) {
	queue, fake := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup, RunAfter: fake.Now().Add(time.Hour)})

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatal("a job scheduled for later was claimed now")
	}

	fake.Advance(time.Hour)
	if later := claimOne(t, queue, jobs.PoolMaintenance, "worker-1"); later.ID == 0 {
		t.Error("the job was not claimable once its time came")
	}
}

func TestClaim_WaitsForADependency(t *testing.T) {
	queue, _ := newQueue(t)

	parent := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup, DependsOn: parent.ID})

	// Only the parent is claimable: a dependent job stays unclaimable rather
	// than being claimed and discovering the problem inside a worker.
	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != parent.ID {
		t.Fatalf("claimed %+v, want only the parent %d", claimed, parent.ID)
	}

	attempt := startJob(t, queue, claimed[0], "worker-1")
	if err := queue.Complete(t.Context(), claimed[0], "worker-1", attempt); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	after, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("Claim after the parent succeeded: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("claimed %d jobs after the dependency succeeded, want 1", len(after))
	}
}

func TestComplete_RecordsTheAttempt(t *testing.T) {
	queue, _ := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	claimed := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	attempt := startJob(t, queue, claimed, "worker-1")
	if err := queue.Complete(t.Context(), claimed, "worker-1", attempt); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	finished, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if finished.State != jobs.StateSucceeded {
		t.Errorf("State = %q, want SUCCEEDED", finished.State)
	}
	if finished.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", finished.Attempts)
	}
	if finished.LeaseOwner != "" {
		t.Errorf("LeaseOwner = %q, want it released", finished.LeaseOwner)
	}

	attempts, err := queue.Attempts(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != string(jobs.OutcomeSucceeded) {
		t.Errorf("attempts = %+v, want one succeeded", attempts)
	}
}

// Classification matters more than the count: retrying a 404 spends an hour of
// exponential backoff reaching the same conclusion.
func TestFail_TerminalErrorDoesNotConsumeTheRetryBudget(t *testing.T) {
	queue, _ := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	claimed := claimOne(t, queue, jobs.PoolImport, "worker-1")
	attempt := startJob(t, queue, claimed, "worker-1")

	resolution, err := queue.Fail(t.Context(), claimed, "worker-1", attempt,
		jobs.Terminalf("import.run", "acquisition 42", errors.New("no media files in the download")))
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if resolution.Class != jobs.ClassTerminal || resolution.Retrying {
		t.Errorf("resolution = %+v, want terminal and not retrying", resolution)
	}

	failed, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if failed.State != jobs.StateFailed {
		t.Errorf("State = %q after a terminal error, want FAILED", failed.State)
	}
	if failed.Attempts != 1 {
		t.Errorf("Attempts = %d; a terminal error must not burn the budget", failed.Attempts)
	}
}

func TestFail_RetryableErrorGoesBackWithBackoff(t *testing.T) {
	queue, fake := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	claimed := claimOne(t, queue, jobs.PoolImport, "worker-1")
	attempt := startJob(t, queue, claimed, "worker-1")

	resolution, err := queue.Fail(t.Context(), claimed, "worker-1", attempt,
		jobs.Retryablef("import.run", "acquisition 42", errors.New("connection reset")))
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if !resolution.Retrying {
		t.Fatal("a retryable error was not retried")
	}

	retried, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if retried.State != jobs.StatePendingRetry {
		t.Errorf("State = %q, want PENDING_RETRY", retried.State)
	}
	// NoJitter, so the first backoff is exactly the 30s base.
	if want := fake.Now().Add(30 * time.Second); !retried.RunAfter.Equal(want) {
		t.Errorf("RunAfter = %v, want %v", retried.RunAfter, want)
	}
	if retried.LastError == "" {
		t.Error("LastError is empty; the reason for the retry is not recorded")
	}
}

func TestFail_BackoffGrowsAndStopsAtTheAttemptCap(t *testing.T) {
	queue, fake := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeImport}) // MaxAttempts 3

	var delays []time.Duration
	for attemptNumber := 1; attemptNumber <= 3; attemptNumber++ {
		claimed := claimOne(t, queue, jobs.PoolImport, "worker-1")
		attempt := startJob(t, queue, claimed, "worker-1")
		resolution, err := queue.Fail(t.Context(), claimed, "worker-1", attempt,
			jobs.Retryablef("import.run", "", errors.New("still broken")))
		if err != nil {
			t.Fatalf("Fail attempt %d: %v", attemptNumber, err)
		}
		if resolution.Retrying {
			delays = append(delays, resolution.RunAfter.Sub(fake.Now()))
			fake.Advance(resolution.RunAfter.Sub(fake.Now()))
		}
	}

	if len(delays) != 2 {
		t.Fatalf("retried %d times, want 2 before the cap of 3 attempts", len(delays))
	}
	if delays[1] <= delays[0] {
		t.Errorf("backoff did not grow: %v then %v", delays[0], delays[1])
	}

	exhausted, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if exhausted.State != jobs.StateFailed {
		t.Errorf("State = %q after exhausting attempts, want FAILED", exhausted.State)
	}
	if exhausted.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", exhausted.Attempts)
	}
}

// A crashed process leaves leases that expire, and the next reaper pass puts
// the work back with no manual cleanup.
func TestReclaimExpired_ReturnsAbandonedWork(t *testing.T) {
	queue, fake := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	claimed := claimOne(t, queue, jobs.PoolMaintenance, "crashed-worker")
	if _, err := queue.Start(t.Context(), claimed, "crashed-worker"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Nothing reclaims a live lease.
	reclaimed, err := queue.ReclaimExpired(t.Context())
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if len(reclaimed) != 0 {
		t.Fatalf("reclaimed %d live leases", len(reclaimed))
	}

	fake.Advance(2 * time.Minute)
	reclaimed, err = queue.ReclaimExpired(t.Context())
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != enqueued.ID {
		t.Fatalf("reclaimed %+v, want job %d", reclaimed, enqueued.ID)
	}

	back, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if back.State != jobs.StatePendingRetry {
		t.Errorf("State = %q after reclaim, want PENDING_RETRY", back.State)
	}
	if back.LastError == "" {
		t.Error("LastError is empty; the reason the work reappeared is not recorded")
	}
}

// A worker whose lease was reaped must stop rather than race the worker that
// has the job now.
func TestRenew_FailsOnceTheLeaseHasBeenReclaimed(t *testing.T) {
	queue, fake := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	claimed := claimOne(t, queue, jobs.PoolMaintenance, "slow-worker")
	if _, err := queue.Start(t.Context(), claimed, "slow-worker"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := queue.Renew(t.Context(), claimed, "slow-worker", time.Minute); err != nil {
		t.Fatalf("Renew while held: %v", err)
	}

	fake.Advance(2 * time.Minute)
	if _, err := queue.ReclaimExpired(t.Context()); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}

	if err := queue.Renew(t.Context(), claimed, "slow-worker", time.Minute); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("Renew after reclaim = %v, want ErrLeaseLost", err)
	}
}

func TestComplete_FailsOnceTheLeaseHasBeenReclaimed(t *testing.T) {
	queue, fake := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	claimed := claimOne(t, queue, jobs.PoolMaintenance, "slow-worker")
	attempt := startJob(t, queue, claimed, "slow-worker")

	fake.Advance(2 * time.Minute)
	if _, err := queue.ReclaimExpired(t.Context()); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}

	// The job now belongs to whoever claims it next; a late completion must not
	// mark it succeeded underneath them.
	if err := queue.Complete(t.Context(), claimed, "slow-worker", attempt); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Errorf("Complete after reclaim = %v, want ErrLeaseLost", err)
	}
}

func TestCancel_StopsPendingWork(t *testing.T) {
	queue, _ := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	if err := queue.Cancel(t.Context(), enqueued.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	cancelled, err := queue.Get(t.Context(), enqueued.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cancelled.State != jobs.StateCancelled {
		t.Errorf("State = %q, want CANCELLED", cancelled.State)
	}

	claimed, err := queue.Claim(t.Context(), jobs.PoolMaintenance, "worker-1", 5, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Error("a cancelled job was claimed")
	}
}

func TestCancel_RefusesAFinishedJob(t *testing.T) {
	queue, _ := newQueue(t)
	enqueued := mustEnqueue(t, queue, jobs.Request{Type: typeBackup})

	claimed := claimOne(t, queue, jobs.PoolMaintenance, "worker-1")
	attempt := startJob(t, queue, claimed, "worker-1")
	if err := queue.Complete(t.Context(), claimed, "worker-1", attempt); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if err := queue.Cancel(t.Context(), enqueued.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("Cancel of a finished job = %v, want ErrNotFound", err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want jobs.Class
	}{
		{"explicit terminal", jobs.Terminalf("op", "", errors.New("bad")), jobs.ClassTerminal},
		{"explicit fatal", jobs.Fatalf("op", "", errors.New("bug")), jobs.ClassFatal},
		{"explicit retryable", jobs.Retryablef("op", "", errors.New("timeout")), jobs.ClassRetryable},
		{"wrapped terminal", fmt.Errorf("outer: %w", jobs.Terminalf("op", "", errors.New("bad"))), jobs.ClassTerminal},
		// A cancelled context is a shutdown, not a fault: the work should be
		// picked up again on the next start.
		{"cancellation", context.Canceled, jobs.ClassRetryable},
		{"deadline", context.DeadlineExceeded, jobs.ClassRetryable},
		{"knows it is not retryable", notRetryable{}, jobs.ClassTerminal},
		{"knows it is retryable", isRetryable{}, jobs.ClassRetryable},
		// Transient I/O is the commonest unlabelled failure, and the attempt cap
		// bounds what a misclassification costs.
		{"unlabelled", errors.New("something went wrong"), jobs.ClassRetryable},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := jobs.Classify(testCase.err); got != testCase.want {
				t.Errorf("Classify = %q, want %q", got, testCase.want)
			}
		})
	}
}

type notRetryable struct{}

func (notRetryable) Error() string   { return "404 not found" }
func (notRetryable) Retryable() bool { return false }

type isRetryable struct{}

func (isRetryable) Error() string   { return "503 unavailable" }
func (isRetryable) Retryable() bool { return true }

func TestRegistry_RefusesADuplicateType(t *testing.T) {
	registry := jobs.NewRegistry()
	definition := jobs.Definition{Type: typeBackup, Pool: jobs.PoolMaintenance}

	if err := registry.Register(definition); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	// The second registration would silently change the behaviour of work
	// already queued under the first.
	if err := registry.Register(definition); err == nil {
		t.Error("a duplicate registration was accepted")
	}
}

func TestRegistry_RequiresATypeAndAPool(t *testing.T) {
	registry := jobs.NewRegistry()
	if err := registry.Register(jobs.Definition{Pool: jobs.PoolImport}); err == nil {
		t.Error("a definition with no type was accepted")
	}
	if err := registry.Register(jobs.Definition{Type: typeBackup}); err == nil {
		t.Error("a definition with no pool was accepted")
	}
}

func TestRegistry_ListsTypesAndPools(t *testing.T) {
	queue, _ := newQueue(t)
	_ = queue

	registry := jobs.NewRegistry()
	registry.MustRegister(jobs.Definition{Type: typeImport, Pool: jobs.PoolImport})
	registry.MustRegister(jobs.Definition{Type: typeBackup, Pool: jobs.PoolMaintenance})

	if got := registry.Types(); len(got) != 2 || got[0] != typeBackup {
		t.Errorf("Types() = %v, want sorted [db.backup import.run]", got)
	}
	if got := registry.Pools(); len(got) != 2 {
		t.Errorf("Pools() = %v, want two", got)
	}
}

func TestCounts_ReportsQueueDepthPerPool(t *testing.T) {
	queue, _ := newQueue(t)
	mustEnqueue(t, queue, jobs.Request{Type: typeBackup})
	mustEnqueue(t, queue, jobs.Request{Type: typeImport})

	counts, err := queue.Counts(t.Context())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts[jobs.StatePending][jobs.PoolMaintenance] != 1 {
		t.Errorf("maintenance pending = %d, want 1", counts[jobs.StatePending][jobs.PoolMaintenance])
	}
	if counts[jobs.StatePending][jobs.PoolImport] != 1 {
		t.Errorf("import pending = %d, want 1", counts[jobs.StatePending][jobs.PoolImport])
	}
}
