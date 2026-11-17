package iogate

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

func newGate(fake clock.Clock, capacity int, onWait func(string)) *Gate {
	return New(fake, Options{Capacity: func(string) int { return capacity }, AgeStep: 10 * time.Second, OnWait: onWait})
}

// waitFor starts Acquire in the background and returns the channel its release arrives on.
func waitFor(g *Gate, group string, class Class) <-chan func() {
	out := make(chan func(), 1)
	go func() {
		release, err := g.Acquire(context.Background(), group, class)
		if err == nil {
			out <- release
		}
	}()
	return out
}

func waitingOn(t *testing.T, g *Gate, group string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for g.Waiting(group) != want {
		if time.Now().After(deadline) {
			t.Fatalf("waiting = %d, want %d", g.Waiting(group), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAcquire_TheMostUrgentWaiterGetsTheNextSlot(t *testing.T) {
	g := newGate(clock.NewFakeAtEpoch(), 1, nil)
	first, _ := g.Acquire(context.Background(), "d", ClassImport)
	scan := waitFor(g, "d", ClassScan)
	waitingOn(t, g, "d", 1)
	imp := waitFor(g, "d", ClassImport)
	waitingOn(t, g, "d", 2)

	first()
	select {
	case release := <-imp:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the import must go before the earlier-waiting scan")
	}
	select {
	case release := <-scan:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("the scan must follow once the import is done")
	}
}

func TestAcquire_BulkWorkThatWaitedLongNeverOvertakesUrgentWork(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	g := newGate(fake, 2, nil)
	first, _ := g.Acquire(context.Background(), "d", ClassImport)
	second, _ := g.Acquire(context.Background(), "d", ClassImport)
	previews := waitFor(g, "d", ClassPreviews)
	waitingOn(t, g, "d", 1)
	fake.Advance(95 * time.Second) // nine steps: previews would count as playback without the floor
	rename := waitFor(g, "d", ClassRename)
	waitingOn(t, g, "d", 2)

	first()
	select {
	case release := <-rename:
		release()
	case <-time.After(2 * time.Second):
		t.Fatal("a rename must go before bulk work however long that waited")
	}
	second()
	release := <-previews
	release()
}

func TestAcquire_PlaybackNeverWaitsForTheSlotsOfOtherWork(t *testing.T) {
	g := newGate(clock.NewFakeAtEpoch(), 2, nil)
	first, _ := g.Acquire(context.Background(), "d", ClassImport)
	second, _ := g.Acquire(context.Background(), "d", ClassDrain)
	defer first()
	defer second()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := g.Acquire(ctx, "d", ClassPlayback)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if g.Waiting("d") != 0 {
		t.Fatal("playback waited behind two copies")
	}
}

func TestAcquire_PlaybackHoldersAreCapped(t *testing.T) {
	g := newGate(clock.NewFakeAtEpoch(), 1, nil)
	var releases []func()
	for range maxPlaybackHolders {
		release, err := g.Acquire(context.Background(), "d", ClassPlayback)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	extra := waitFor(g, "d", ClassPlayback)
	waitingOn(t, g, "d", 1)
	releases[0]()
	release := <-extra
	release()
	for _, release := range releases[1:] {
		release()
	}
}

func TestAcquire_AShrunkDiskStillAdmitsOneUrgentTaskBesideBulkWork(t *testing.T) {
	g := newGate(clock.NewFakeAtEpoch(), 1, nil)
	scan, _ := g.Acquire(context.Background(), "d", ClassScan)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	imp, err := g.Acquire(ctx, "d", ClassImport)
	if err != nil {
		t.Fatal(err)
	}
	second := waitFor(g, "d", ClassRename)
	waitingOn(t, g, "d", 1)
	imp()
	release := <-second
	release()
	scan()
}

func TestAcquire_BulkWorkLeavesOneSlotForUrgentWork(t *testing.T) {
	g := newGate(clock.NewFakeAtEpoch(), 2, nil)
	scan, _ := g.Acquire(context.Background(), "d", ClassScan)
	second := waitFor(g, "d", ClassProbe)
	waitingOn(t, g, "d", 1)
	imp, err := g.Acquire(context.Background(), "d", ClassImport)
	if err != nil {
		t.Fatal(err)
	}
	if g.Waiting("d") != 1 {
		t.Fatal("the second bulk task must keep waiting while the last slot is for urgent work")
	}
	imp()
	scan()
	release := <-second
	release()
}

func TestAcquire_GroupsDoNotShareSlotsAndWaitingIsAnnounced(t *testing.T) {
	var announced []string
	g := newGate(clock.NewFakeAtEpoch(), 1, func(group string) { announced = append(announced, group) })
	a, _ := g.Acquire(context.Background(), "a", ClassScan)
	b, _ := g.Acquire(context.Background(), "b", ClassScan)
	if len(announced) != 0 {
		t.Fatalf("announced %v, want nothing: nobody waited", announced)
	}
	blocked := waitFor(g, "a", ClassProbe)
	waitingOn(t, g, "a", 1)
	a()
	release := <-blocked
	release()
	b()
	if len(announced) != 1 || announced[0] != "a" {
		t.Fatalf("announced %v, want [a]", announced)
	}
}

func TestAcquire_ACancelledWaiterLeavesTheQueueAndIdleFollows(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	g := newGate(fake, 1, nil)
	held, _ := g.Acquire(context.Background(), "d", ClassScan)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := g.Acquire(ctx, "d", ClassProbe); done <- err }()
	waitingOn(t, g, "d", 1)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a cancelled wait must fail")
	}
	if g.Waiting("d") != 0 {
		t.Fatal("the cancelled waiter must leave the queue")
	}
	held()
	if g.Idle("d", time.Minute) {
		t.Fatal("a group used a moment ago is not idle")
	}
	fake.Advance(2 * time.Minute)
	if !g.Idle("d", time.Minute) {
		t.Fatal("a group untouched for the window is idle")
	}
}

type countingTurns struct{ taken, given int }

func (c *countingTurns) AcquirePaths(context.Context, string, []string) (func(), error) {
	c.taken++
	return func() { c.given++ }, nil
}

func TestReader_EveryReadIsATurnAndNothingIsHeldBetweenReads(t *testing.T) {
	turns := &countingTurns{}
	ctx := WithTurns(context.Background(), turns)
	reader := Reader(ctx, "playback", "/lib/a.mkv", strings.NewReader("abcdef"))
	buffer := make([]byte, 4)
	n, err := reader.Read(buffer)
	if err != nil || n != 4 || string(buffer[:n]) != "abcd" {
		t.Fatalf("read %q err %v", buffer[:n], err)
	}
	if turns.taken != 1 || turns.given != 1 {
		t.Fatalf("turns taken %d given %d, want 1 and 1: the turn ends with the read", turns.taken, turns.given)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil || turns.taken != 1 {
		t.Fatalf("a seek reads nothing and takes no turn: err %v taken %d", err, turns.taken)
	}
}
