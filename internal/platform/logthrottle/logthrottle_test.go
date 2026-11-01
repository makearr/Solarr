package logthrottle_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/logthrottle"
)

type paused struct{ provider string }

func (e paused) Error() string       { return "paused " + e.provider }
func (e paused) ThrottleKey() string { return e.provider }

func lines(out *bytes.Buffer) []string {
	return strings.Split(strings.TrimSpace(out.String()), "\n")
}

func TestARepeatedKeyedRecordIsLoggedOncePerWindowWithTheCountLeftOut(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	var out bytes.Buffer
	logger := slog.New(logthrottle.New(slog.NewTextHandler(&out, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}), fake, time.Minute))

	for range 5 {
		logger.Warn("skipped", slog.Any("error", fmt.Errorf("wrapped: %w", paused{"caa"})))
	}
	logger.Warn("skipped", slog.Any("error", paused{"other"}))
	if got := lines(&out); len(got) != 2 {
		t.Fatalf("lines inside the window = %d (%q), want 2: one per provider", len(got), got)
	}

	fake.Advance(time.Minute)
	logger.Warn("skipped", slog.Any("error", paused{"caa"}))
	got := lines(&out)
	if len(got) != 3 || !strings.Contains(got[2], "suppressed=4") {
		t.Errorf("after the window: %q, want a third line saying suppressed=4", got)
	}
}

func TestRecordsWithoutAKeyAreNeverThrottled(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(logthrottle.New(slog.NewTextHandler(&out, nil), clock.NewFakeAtEpoch(), time.Minute))
	for range 3 {
		logger.Warn("failed", slog.Any("error", errors.New("plain")))
		logger.Warn("slow", slog.Any("key", logthrottle.Key("write")))
	}
	if got := len(lines(&out)); got != 4 {
		t.Errorf("lines = %d, want 3 plain + 1 keyed", got)
	}
}
