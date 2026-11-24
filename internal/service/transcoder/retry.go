package transcoder

import "time"

// The retry numbers.
const (
	maxAttempts   = 3
	streakToProbe = 3
	probeFiles    = 3
	backoffBase   = 5 * time.Minute
	backoffMax    = 6 * time.Hour
)

// ItemState is the part of a transcode item the retry rules read and change.
type ItemState struct {
	State       string
	Attempts    int
	Pass        int
	Interrupted bool
	FailureCode string
}

// Item states the rules move between.
const (
	StateNeeded = "needed"
	StateFailed = "failed"
)

// HealthMode is how the pipeline of one kind stands.
type HealthMode string

// The modes: ok runs normally, probing tries files far away, backoff waits.
const (
	ModeOK      HealthMode = "ok"
	ModeProbing HealthMode = "probing"
	ModeBackoff HealthMode = "backoff"
)

// Health is how a kind of conversion is doing: a broken system must not burn files into Failed.
type Health struct {
	ConsecutiveFailures int
	Mode                HealthMode
	ProbesLeft          int
	BackoffStep         int
	BackoffUntil        time.Time
	// LastFailedID is the file that failed last; the same file failing again says nothing new about the system.
	LastFailedID int64
}

// FailureError is the failure code of an item that ran out of attempts.
const FailureError = "error"

// BackoffFor is the wait after the step-th round of failed probes: 5 minutes doubling, capped at 6 hours.
func BackoffFor(step int) time.Duration {
	wait := backoffBase
	for range step {
		wait *= 2
		if wait >= backoffMax {
			return backoffMax
		}
	}
	return wait
}

// AfterError records a conversion that errored (not one whose result was refused).
func AfterError(item ItemState, health Health, itemID int64, now time.Time) (ItemState, Health) {
	if health.Mode == "" {
		health.Mode = ModeOK
	}
	if itemID != health.LastFailedID {
		health.ConsecutiveFailures++
	}
	health.LastFailedID = itemID
	item.Interrupted = false
	item.State = StateNeeded
	if health.Mode == ModeOK {
		item = countAttempt(item, FailureError)
		if health.ConsecutiveFailures >= streakToProbe {
			health.Mode, health.ProbesLeft = ModeProbing, probeFiles
		}
		return item, health
	}
	health.ProbesLeft--
	if health.ProbesLeft <= 0 {
		health.Mode, health.ProbesLeft = ModeBackoff, 0
		health.BackoffUntil = now.Add(BackoffFor(health.BackoffStep))
		health.BackoffStep++
	}
	return item, health
}

// countAttempt spends one of the item's attempts: it needs another run, or has failed with the code.
func countAttempt(item ItemState, code string) ItemState {
	item.Attempts++
	if item.Attempts >= maxAttempts {
		item.State, item.FailureCode = StateFailed, code
	}
	return item
}

// AfterSuccess clears the failure streak; a result that was refused after a working encode counts too.
func AfterSuccess() Health {
	return Health{Mode: ModeOK}
}

// AfterInterruption puts back an item whose run was cut off by a shutdown or a pause; it goes first next time.
func AfterInterruption(item ItemState) ItemState {
	item.Interrupted, item.State = true, StateNeeded
	return item
}

// AfterBackoff ends a wait that has passed with one probe.
func AfterBackoff(health Health, now time.Time) Health {
	if health.Mode == ModeBackoff && !now.Before(health.BackoffUntil) {
		health.Mode, health.ProbesLeft, health.BackoffUntil = ModeProbing, 1, time.Time{}
	}
	return health
}

// ResumeNow is "try now": the wait is skipped for one probe.
func ResumeNow(health Health) Health {
	health.Mode, health.ProbesLeft, health.BackoffUntil = ModeProbing, 1, time.Time{}
	return health
}

// AfterRefused records a finished conversion the checks refused: another pass when the ladder has one, else an attempt
// (a failure that is not about size) or the end (a size the ladder could not reach).
func AfterRefused(item ItemState, failures []Reason, next PassSettings, laddered bool) ItemState {
	item.Interrupted, item.State = false, StateNeeded
	switch {
	case laddered:
		item.Pass = next.Pass
	case Ladderable(failures):
		item.State, item.FailureCode = StateFailed, string(failures[0].Code)
	default:
		item = countAttempt(item, string(failures[0].Code))
	}
	return item
}
