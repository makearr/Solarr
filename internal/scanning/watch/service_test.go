package watch

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fswatch"
)

type fakeNotifier struct {
	added  []string
	events chan fswatch.Event
	limit  int
}

func (f *fakeNotifier) Add(dir string) error {
	if f.limit > 0 && len(f.added) >= f.limit {
		return fswatch.ErrLimit
	}
	f.added = append(f.added, dir)
	return nil
}
func (f *fakeNotifier) Remove(string)                {}
func (f *fakeNotifier) Count() int                   { return len(f.added) }
func (f *fakeNotifier) Events() <-chan fswatch.Event { return f.events }
func (f *fakeNotifier) Close() error                 { return nil }

type dirtyCall struct {
	root int64
	dirs []string
}

func newService(t *testing.T, budget int, trusted bool) (*Service, *fakeNotifier, *clock.Fake, *[]dirtyCall) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	notifier := &fakeNotifier{events: make(chan fswatch.Event)}
	var calls []dirtyCall
	service := New(Config{
		Notifier: notifier, Clock: fake,
		Roots: func() []Root { return []Root{{ID: 1, Path: "/lib", Trusted: trusted}} },
		Directories: func(context.Context, int64) ([]DirRecord, error) {
			return []DirRecord{{"A", 10}, {"A/s1", 5}, {"B", 20}, {"B/s1", 1}}, nil
		},
		Budget: func() int { return budget },
		Dirty: func(_ context.Context, root int64, dirs []string) {
			sort.Strings(dirs)
			calls = append(calls, dirtyCall{root, dirs})
		},
		Quiet: 10 * time.Second, MaxWait: time.Minute,
	})
	return service, notifier, fake, &calls
}

func TestReplan_WatchesTheShallowLevelsWithinTheBudget(t *testing.T) {
	service, notifier, _, _ := newService(t, 4, true)
	if err := service.Replan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/lib", "/lib/B", "/lib/A", "/lib/A/s1"}; !reflect.DeepEqual(notifier.added, want) {
		t.Fatalf("watched %v, want %v", notifier.added, want)
	}
	if status := service.Status(); status.Watches != 4 || status.Directories != 5 || !status.BudgetShort {
		t.Fatalf("status = %+v, want 4 of 5 and short", status)
	}
}

func TestEvents_AreRescannedOnceQuietAndABusyDirectoryDoesNotWaitForever(t *testing.T) {
	service, _, fake, calls := newService(t, 100, true)
	if err := service.Replan(t.Context()); err != nil {
		t.Fatal(err)
	}
	service.handle(fswatch.Event{Dir: "/lib/A", Name: "new.flac", Created: true})
	service.handle(fswatch.Event{Dir: "/lib/A", Name: "new.flac.partial", Created: true})
	service.handle(fswatch.Event{Dir: "/lib/unwatched", Name: "x", Created: true})
	fake.Advance(5 * time.Second)
	service.Flush(t.Context())
	if len(*calls) != 0 {
		t.Fatalf("flushed %v before the directory was quiet", *calls)
	}
	fake.Advance(6 * time.Second)
	service.Flush(t.Context())
	if want := []dirtyCall{{1, []string{"A"}}}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("flushed %v, want %v", *calls, want)
	}

	*calls = nil
	for range 8 {
		service.handle(fswatch.Event{Dir: "/lib/B", Name: "f", Created: true})
		fake.Advance(9 * time.Second)
	}
	service.Flush(t.Context())
	if len(*calls) != 1 {
		t.Fatalf("a directory that never goes quiet must still be rescanned after MaxWait, got %v", *calls)
	}
}

func TestEvents_ANewDirectoryIsWatchedAndRescanned(t *testing.T) {
	service, notifier, fake, calls := newService(t, 100, true)
	_ = service.Replan(t.Context())
	service.handle(fswatch.Event{Dir: "/lib", Name: "C", IsDir: true, Created: true})
	if notifier.added[len(notifier.added)-1] != "/lib/C" {
		t.Fatalf("watched %v, want /lib/C added", notifier.added)
	}
	fake.Advance(time.Minute)
	service.Flush(t.Context())
	if want := []dirtyCall{{1, []string{"", "C"}}}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("flushed %v, want %v", *calls, want)
	}
}

func TestTrust_NeedsAWatchOlderThanTheScanNoLostEventsAndAFilesystemItSeesThrough(t *testing.T) {
	service, _, fake, _ := newService(t, 100, true)
	_ = service.Replan(t.Context())
	root := Root{ID: 1, Path: "/lib", Trusted: true}
	registered := fake.Now()

	fake.Advance(time.Hour)
	scan := fake.Now()
	if watching, depth := service.Trust(root, scan); !watching || depth != 2 {
		t.Fatalf("after a scan that followed the watch: %v %d, want true 2", watching, depth)
	}
	if watching, _ := service.Trust(root, registered.Add(-time.Hour)); watching {
		t.Fatal("a scan that predates the watch proves nothing about changes in between")
	}
	if watching, _ := service.Trust(Root{ID: 1, Path: "/lib", Trusted: false}, scan); watching {
		t.Fatal("a network share is not trusted")
	}
	fake.Advance(time.Minute)
	service.handle(fswatch.Event{Overflow: true})
	if watching, _ := service.Trust(root, scan); watching {
		t.Fatal("lost events after the scan must end the trust")
	}
	if watching, _ := service.Trust(root, fake.Now().Add(time.Second)); !watching {
		t.Fatal("a scan after the loss restores it")
	}
}
