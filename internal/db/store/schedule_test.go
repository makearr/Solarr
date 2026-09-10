package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
)

func TestNewBackupSchedule_RejectsAnInvalidExpression(t *testing.T) {
	fixture := newMigrationFixture(t)

	if _, err := store.NewBackupSchedule(
		fixture.store, "not a cron expression", fixture.backupDir, 5, fixture.clock, quietLogger(),
	); err == nil {
		t.Error("an invalid cron expression was accepted")
	}
}

// The schedule fires on its own clock, not on construction or on the caller polling
// it -- Run blocks until the fake clock is advanced to the next scheduled minute.
func TestBackupSchedule_FiresAtTheScheduledTime(t *testing.T) {
	fixture := newMigrationFixture(t)
	schedule, err := store.NewBackupSchedule(
		fixture.store, "* * * * *", fixture.backupDir, 5, fixture.clock, quietLogger(),
	)
	if err != nil {
		t.Fatalf("NewBackupSchedule: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(time.Minute)

	waitForBackups(t, fixture, 1)

	cancel()
	<-done
}

// Every fire prunes too, or rotation backups accumulate forever -- the whole point of
// this subsystem existing. Four are seeded so the enforced minimum of 3 (retention.go)
// cannot explain the result by itself: pruning has to actually run to reach it.
func TestBackupSchedule_PrunesOnEveryFire(t *testing.T) {
	fixture := newMigrationFixture(t)
	for range 4 {
		if _, err := fixture.store.Backup(t.Context(), fixture.backupDir, store.BackupRotation); err != nil {
			t.Fatalf("seed backup: %v", err)
		}
		fixture.clock.Advance(time.Hour)
	}

	schedule, err := store.NewBackupSchedule(
		fixture.store, "* * * * *", fixture.backupDir, 1, fixture.clock, quietLogger(),
	)
	if err != nil {
		t.Fatalf("NewBackupSchedule: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	fixture.clock.BlockUntilWaiters(1)
	fixture.clock.Advance(time.Minute)

	// Five rotation backups existed for a moment (four seeded plus this fire's own);
	// PruneBackups clamps keep=1 to the enforced minimum of 3, so that is what should
	// remain once pruning has run -- if it has not, five would still be on disk.
	waitForBackups(t, fixture, 3)

	cancel()
	<-done
}

func TestBackupSchedule_StopsWhenContextEnds(t *testing.T) {
	fixture := newMigrationFixture(t)
	schedule, err := store.NewBackupSchedule(
		fixture.store, "* * * * *", fixture.backupDir, 5, fixture.clock, quietLogger(),
	)
	if err != nil {
		t.Fatalf("NewBackupSchedule: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		schedule.Run(ctx)
	}()

	fixture.clock.BlockUntilWaiters(1)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

// waitForBackups polls ListBackups rather than sleeping a fixed amount: the fire
// itself is real goroutine scheduling behind a fake clock, not something Advance
// blocks until finished.
func waitForBackups(t *testing.T, fixture migrationFixture, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		found, err := fixture.store.ListBackups(fixture.backupDir)
		if err != nil {
			t.Fatalf("ListBackups: %v", err)
		}
		if len(found) == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("backups = %d after 5s, want %d", len(found), want)
		case <-time.After(time.Millisecond):
		}
	}
}
