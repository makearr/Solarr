package settings

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeyReadyQueueLimit bounds section 10.5's ready queue of held releases.
const KeyReadyQueueLimit = "acquire.ready_queue_limit"

// Ready queue limits: priming ten thousand releases would only move the problem.
const (
	DefaultReadyQueueLimit = 200
	MaxReadyQueueLimit     = 10000
)

// ReadyQueueLimit reads how many releases may be held at once, or the default when nothing is stored.
func (s *Snapshot) ReadyQueueLimit() int64 {
	limit := s.policyInt(KeyReadyQueueLimit, DefaultReadyQueueLimit)
	if limit < 1 || limit > MaxReadyQueueLimit {
		return DefaultReadyQueueLimit
	}
	return limit
}

// SetReadyQueueLimit writes the limit, validated on write (ADR-44).
func (c *Cache) SetReadyQueueLimit(ctx context.Context, cl clock.Clock, limit int64) error {
	if limit < 1 || limit > MaxReadyQueueLimit {
		return config.Problems{{Path: KeyReadyQueueLimit, Value: strconv.FormatInt(limit, 10),
			Expectation: "1 to " + strconv.Itoa(MaxReadyQueueLimit)}}
	}
	return c.setPolicies(ctx, cl, map[string]string{KeyReadyQueueLimit: strconv.FormatInt(limit, 10)})
}
