package httpx

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ReadLimited reads a response body up to limit bytes and rejects a response
// that has more data, rather than treating a truncated body as complete.
func ReadLimited(body io.Reader, limit int64) ([]byte, error) {
	if body == nil {
		return nil, fmt.Errorf("httpx: nil response body")
	}
	if limit < 0 || limit == int64(^uint64(0)>>1) {
		return nil, fmt.Errorf("httpx: invalid response limit %d", limit)
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("httpx: response exceeds %d bytes", limit)
	}
	return data, nil
}

// ResponseClass decides behaviour, which is the part that matters (§16).
type ResponseClass string

const (
	// ClassSuccess is a usable response.
	ClassSuccess ResponseClass = "success"
	// ClassRetryable is the provider being unavailable or throttling us. It
	// counts towards backoff and the circuit breaker.
	ClassRetryable ResponseClass = "retryable"
	// ClassTerminal is our request being wrong — a bad key, a missing resource.
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
		maxSeconds := (1<<63 - 1) / int64(time.Second)
		if int64(seconds) > maxSeconds {
			return time.Duration(1<<63 - 1)
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
