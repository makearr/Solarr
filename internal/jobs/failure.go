package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Class decides what happens after a failure (section 16).
type Class string

// The three classes of architecture section 16.
const (
	// ClassRetryable is a condition that may pass: a timeout, a 429, a 503, a
	// connection reset, a busy database.
	ClassRetryable Class = "retryable"
	// ClassTerminal is a condition that will not: a parse failure, a 404, permission denied,
	// invalid configuration.
	ClassTerminal Class = "terminal"
	// ClassFatal is a programming error or a violated invariant. It fails and
	// is reported loudly; retrying could only repeat it.
	ClassFatal Class = "fatal"
)

// Error carries a classification with a failure. A job that returns one decides
// its own fate; anything else is classified by Classify.
type Error struct {
	Class   Class
	Op      string
	Subject string
	Cause   error
}

func (e *Error) Error() string {
	subject := e.Subject
	if subject != "" {
		subject = " " + subject
	}
	return fmt.Sprintf("%s%s: %v", e.Op, subject, e.Cause)
}

func (e *Error) Unwrap() error { return e.Cause }

// Retryablef builds a retryable failure.
func Retryablef(op, subject string, cause error) *Error {
	return &Error{Class: ClassRetryable, Op: op, Subject: subject, Cause: cause}
}

// Terminalf builds a failure that will not pass.
func Terminalf(op, subject string, cause error) *Error {
	return &Error{Class: ClassTerminal, Op: op, Subject: subject, Cause: cause}
}

// Fatalf builds a failure that means the program is wrong.
func Fatalf(op, subject string, cause error) *Error {
	return &Error{Class: ClassFatal, Op: op, Subject: subject, Cause: cause}
}

// retryable is implemented by errors that already know their own nature, such as
// httpx.StatusError.
type retryable interface{ Retryable() bool }

// Classify decides how to treat a failure.
func Classify(err error) Class {
	if err == nil {
		return ClassRetryable
	}

	var classified *Error
	if errors.As(err, &classified) {
		return classified.Class
	}

	// A cancelled context is a shutdown, not a fault: the work should be picked
	// up again on the next start rather than counted as a failure.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ClassRetryable
	}

	var knows retryable
	if errors.As(err, &knows) {
		if knows.Retryable() {
			return ClassRetryable
		}
		return ClassTerminal
	}
	return ClassRetryable
}

// Backoff defaults from architecture section 7.2.
const (
	backoffBase = 30 * time.Second
	backoffCap  = 6 * time.Hour
)

// backoffFor returns min(base * 2^attempt, cap). The caller applies jitter.
func backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Shift rather than math.Pow, and clamp before the exponent overflows.
	if attempt > 20 {
		return backoffCap
	}
	backoff := backoffBase << (attempt - 1)
	if backoff > backoffCap || backoff <= 0 {
		return backoffCap
	}
	return backoff
}
