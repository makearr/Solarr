package app

import (
	"context"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/disklatency"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
)

type fakeLayout map[string]string

func (f fakeLayout) GroupOfPath(path string) string { return f[path] }
func (f fakeLayout) GroupOfRoot(id int64) (string, string, bool) {
	return "disk:root", "/root", id == 1
}
func (f fakeLayout) PathOfGroup(string) (string, bool) { return "", false }

func newTestTurns(layout fakeLayout) *diskTurns {
	fake := clock.NewFakeAtEpoch()
	return newDiskTurns(layout, disklatency.New(fsx.NewMem(fake), fake, time.Second), fake)
}

func TestAcquirePaths_BatchesNeedingTheSameDisksInOppositeOrderDoNotDeadlock(t *testing.T) {
	turns := newTestTurns(fakeLayout{"/a": "disk:a", "/b": "disk:b"})
	job := jobs.Job{Type: "library.probe"}
	done := make(chan struct{}, 2)
	for _, paths := range [][]string{{"/a", "/b"}, {"/b", "/a"}} {
		go func() {
			for range 50 {
				release, err := turns.TurnsFor(job).AcquirePaths(context.Background(), "", paths)
				if err != nil {
					t.Error(err)
					return
				}
				release()
			}
			done <- struct{}{}
		}()
	}
	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("two batches needing the same pair of disks in opposite order deadlocked")
		}
	}
}

func TestAcquire_OnlyWholeRunJobsHoldADiskForTheirRunAndUnlistedJobsAreNeverHeld(t *testing.T) {
	turns := newTestTurns(fakeLayout{})
	ctx := context.Background()
	if turns.gate.Waiting("disk:root") != 0 {
		t.Fatal("nothing should wait yet")
	}
	scan, err := turns.Acquire(ctx, jobs.Job{Type: "library.scan", Payload: []byte(`{"root_folder_id":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if turns.gate.Idle("disk:root", time.Hour) {
		t.Fatal("a scan of root 1 must hold a slot on that root's disk")
	}
	scan()
	probe, err := turns.Acquire(ctx, jobs.Job{Type: "library.probe"})
	if err != nil {
		t.Fatal(err)
	}
	probe()
	other, err := turns.Acquire(ctx, jobs.Job{Type: "acquire.hunt"})
	if err != nil {
		t.Fatal(err)
	}
	other()
	if !turns.gate.Idle(unknownDiskGroup, time.Hour) {
		t.Fatal("per-batch and unlisted jobs must not take the shared group for their run")
	}
}

var _ iogate.Turns = jobTurns{}
