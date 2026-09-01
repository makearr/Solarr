// Package jobs is the durable work queue. Anything that must happen is a job,
// and jobs are persistent (Invariant 22, ADR-9); events are notification only
// and may be dropped.
package jobs

import "time"

// State is where a job sits in the lifecycle of architecture section 7.2.
type State string

// The job states. PENDING_RETRY is distinct from PENDING so a first attempt and
// a retry are distinguishable in the queue without reading the attempt count.
const (
	StatePending      State = "PENDING"
	StatePendingRetry State = "PENDING_RETRY"
	StateLeased       State = "LEASED"
	StateRunning      State = "RUNNING"
	StateSucceeded    State = "SUCCEEDED"
	StateFailed       State = "FAILED"
	StateCancelled    State = "CANCELLED"
)

// Terminal reports whether a state can still change on its own.
func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// Type names a kind of work, as in architecture section 7.1: "db.backup",
// "import.run", "search.group".
type Type string

// Pool is the worker pool a type runs in. Concurrency is per pool, which is how
// a library scan cannot starve an interactive search.
type Pool string

// The pools of architecture section 3.
const (
	PoolSearch      Pool = "search"
	PoolAcquire     Pool = "acquire"
	PoolImport      Pool = "import"
	PoolMetadata    Pool = "metadata"
	PoolMaintenance Pool = "maintenance"
)

// Priority orders work within a pool. Higher runs first.
type Priority int

// Priorities. These order the job queue, and are separate from the write
// queue's priorities in internal/store, which order database transactions.
const (
	PriorityLow         Priority = 10
	PriorityNormal      Priority = 50
	PriorityInteractive Priority = 100
)

// Job is one unit of durable work.
type Job struct {
	ID   int64
	Type Type
	Pool Pool
	// ActionKey deduplicates: at most one active job may carry a given key
	// (Invariant 16). Empty means this work is not deduplicated.
	ActionKey string
	Priority  Priority
	Payload   []byte
	State     State
	// Mutating decides whether this runs in observe mode. The dry-run gate is
	// enforced at the executor and nowhere else (ADR-20, Invariant 31).
	Mutating    bool
	Attempts    int
	MaxAttempts int
	// RunAfter is the earliest time this may be claimed: backoff, or a delay.
	RunAfter       time.Time
	LeaseOwner     string
	LeaseExpiresAt time.Time
	// DependsOn keeps a job unclaimable until another has succeeded.
	DependsOn     int64
	CorrelationID string
	UserID        int64
	// CategoryID scopes the job for per-category visibility (ADR-27). Zero
	// means system work that belongs to no one category.
	CategoryID int64
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Definition declares how a job type behaves. It is registered once at startup
// rather than passed at every enqueue, so two callers cannot disagree about
// whether a type mutates.
type Definition struct {
	Type Type
	Pool Pool
	// Mutating must be true for anything that changes the library, the
	// filesystem or a download client. Getting it wrong makes observe mode a
	// lie, which is worse than not having observe mode.
	Mutating bool
	// MaxAttempts caps retries. Zero takes the default.
	MaxAttempts int
}

// Request is what a caller submits.
type Request struct {
	Type      Type
	ActionKey string
	Priority  Priority
	Payload   []byte
	// RunAfter delays the first attempt. Zero means immediately.
	RunAfter      time.Time
	DependsOn     int64
	CorrelationID string
	UserID        int64
	CategoryID    int64
}

// Attempt is one recorded run, so a job that failed four times can be explained
// rather than just counted.
type Attempt struct {
	ID         int64
	JobID      int64
	Attempt    int
	LeaseOwner string
	StartedAt  time.Time
	FinishedAt time.Time
	Outcome    string
	Error      string
}
