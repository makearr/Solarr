package store

import (
	"log/slog"
	"testing"
	"time"
)

// Every test store fails on an unbounded transaction, whether or not the test wired OnBudgetExceeded (W4).
func TestReportDuration_FailsATestRunOnlyPastTheCeiling(t *testing.T) {
	opened := &Store{config: Config{Name: "library", WriteWarn: time.Millisecond, WriteHard: time.Millisecond}, logger: slog.New(slog.DiscardHandler)}

	opened.reportDuration(&writeOp{}, testWriteCeiling-time.Second, 0)

	defer func() {
		if recover() == nil {
			t.Error("a transaction past the ceiling did not fail the run")
		}
	}()
	opened.reportDuration(&writeOp{}, testWriteCeiling, 0)
}
