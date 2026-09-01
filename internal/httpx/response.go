package httpx

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ResponseClass decides behaviour, which is the part that matters (§16).
// Retrying a 404 spends an hour of exponential backoff reaching the same
// conclusion, so the class is derived from the status, not from an attempt count.
type ResponseClass string

const (
	// ClassSuccess is a usable response.
	ClassSuccess ResponseClass = "success"
	// ClassRetryable is the provider being unavailable or throttling us. It
	// counts towards backoff and the circuit breaker.
	ClassRetryable ResponseClass = "retryable"
	// ClassTerminal is our request being wrong — a bad key, a missing resource.
	// Retrying cannot help, and it must not open the breaker: a 404 says nothing
	// about the provider's health.
	ClassTerminal ResponseClass = "terminal"
)

// ClassifyStatus maps an HTTP status onto its class.
func ClassifyStatus(status int) ResponseClass {
	switch {
	case status >= 200 && status < 300:
		return ClassSuccess
	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
		return ClassRetryable
	case status >= 500:
		return ClassRetryable
	default:
		return ClassTerminal
	}
}

// StatusError reports a non-success response.
type StatusError struct {
	Provider   string
	URL        string
	Status     int
	Class      ResponseClass
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("httpx: %s returned %d for %s (%s)", e.Provider, e.Status, e.URL, e.Class)
}

// Retryable reports whether trying again could plausibly succeed.
func (e *StatusError) Retryable() bool { return e.Class == ClassRetryable }

// ParseRetryAfter reads the header in both permitted forms: delta-seconds, and
// an HTTP-date. A date in the past is no wait at all rather than a negative one.
func ParseRetryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(header)
	if err != nil {
		return 0
	}
	if wait := when.Sub(now); wait > 0 {
		return wait
	}
	return 0
}
