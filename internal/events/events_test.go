package events_test

import (
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/events"
)

func newBus(t *testing.T) (*events.Bus, *clock.Fake) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return events.NewBus(fake), fake
}

func TestBus_DeliversToSubscribers(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe(nil, 4)
	defer subscription.Close()

	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1", Kind: "updated"})

	select {
	case event := <-subscription.Events():
		if event.Entity != "job:1" {
			t.Errorf("Entity = %q, want job:1", event.Entity)
		}
		if event.ID == 0 {
			t.Error("the bus did not assign an id")
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestBus_AssignsMonotonicIDs(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe(nil, 8)
	defer subscription.Close()

	for range 5 {
		bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	}

	var previous uint64
	for range 5 {
		event := <-subscription.Events()
		if event.ID <= previous {
			t.Fatalf("id %d did not increase from %d", event.ID, previous)
		}
		previous = event.ID
	}
}

func TestBus_FiltersByTopic(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe([]events.Topic{events.TopicJob}, 4)
	defer subscription.Close()

	bus.Publish(events.Event{Topic: events.TopicLibrary, Entity: "work:1"})
	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})

	event := <-subscription.Events()
	if event.Topic != events.TopicJob {
		t.Errorf("got a %q event, want only job", event.Topic)
	}
	select {
	case unexpected := <-subscription.Events():
		t.Errorf("a second event arrived: %+v", unexpected)
	default:
	}
}

// Invariant 25: buffers are bounded; overflow drops and counts, never blocks or
// grows. A bus that blocked would make a job's correctness depend on a browser
// tab's speed.
func TestBus_PublishNeverBlocksOnASlowSubscriber(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe(nil, 2)
	defer subscription.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}

	if bus.Dropped() == 0 {
		t.Error("nothing was counted as dropped despite overflowing a buffer of 2")
	}
	if !subscription.Missed() {
		t.Error("the subscriber was not told it had missed events")
	}
}

func TestSubscription_MissedClearsOnRead(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe(nil, 1)
	defer subscription.Close()

	for range 5 {
		bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	}
	if !subscription.Missed() {
		t.Fatal("Missed() = false after an overflow")
	}
	// Cleared, so a client is told to resynchronise once rather than forever.
	if subscription.Missed() {
		t.Error("Missed() stayed true after being read")
	}
}

func TestBus_CloseDetachesTheSubscriber(t *testing.T) {
	bus, _ := newBus(t)
	subscription := bus.Subscribe(nil, 4)

	if got := bus.Subscribers(); got != 1 {
		t.Fatalf("Subscribers() = %d, want 1", got)
	}
	subscription.Close()
	if got := bus.Subscribers(); got != 0 {
		t.Errorf("Subscribers() = %d after Close, want 0", got)
	}
	// A handler that closes on both a normal return and a deferred cleanup is
	// the ordinary shape, so a second Close must not panic.
	subscription.Close()
}

func TestBus_SinceReplaysWhatWasMissed(t *testing.T) {
	bus, _ := newBus(t)
	for range 5 {
		bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	}

	missed, complete := bus.Since(2, nil)
	if !complete {
		t.Error("complete = false with everything still in the ring")
	}
	if len(missed) != 3 {
		t.Fatalf("replayed %d events, want 3", len(missed))
	}
	if missed[0].ID != 3 {
		t.Errorf("first replayed id = %d, want 3", missed[0].ID)
	}
}

// A partial history would turn a lossy notification channel into a source of
// truth it was never meant to be (ADR-9), so an incomplete answer says so.
func TestBus_SinceReportsAnIncompleteHistory(t *testing.T) {
	bus, _ := newBus(t)
	for range events.DefaultReplayBuffer + 50 {
		bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	}

	_, complete := bus.Since(1, nil)
	if complete {
		t.Error("complete = true although the client's position fell out of the ring")
	}
}

func TestBus_SinceFiltersByTopic(t *testing.T) {
	bus, _ := newBus(t)
	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	bus.Publish(events.Event{Topic: events.TopicLibrary, Entity: "work:1"})

	missed, _ := bus.Since(0, map[events.Topic]bool{events.TopicJob: true})
	if len(missed) != 1 || missed[0].Topic != events.TopicJob {
		t.Errorf("replayed %+v, want only the job event", missed)
	}
}

func TestBus_SinceAtTheHeadReturnsNothing(t *testing.T) {
	bus, _ := newBus(t)
	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})

	missed, complete := bus.Since(bus.LastID(), nil)
	if !complete || len(missed) != 0 {
		t.Errorf("Since(head) = (%v, %v), want (empty, complete)", missed, complete)
	}
}

func TestBus_StampsTheTimeFromTheClock(t *testing.T) {
	bus, fake := newBus(t)
	subscription := bus.Subscribe(nil, 2)
	defer subscription.Close()

	fake.Advance(time.Hour)
	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})

	if got := (<-subscription.Events()).At; !got.Equal(fake.Now()) {
		t.Errorf("At = %v, want the injected clock's %v", got, fake.Now())
	}
}

func TestTopic_Valid(t *testing.T) {
	for _, topic := range events.Known() {
		if !topic.Valid() {
			t.Errorf("%q is listed as known but reports invalid", topic)
		}
	}
	if events.Topic("nonsense").Valid() {
		t.Error("an unknown topic reported valid")
	}
}

// Subscribing without naming a topic means every topic, and replay has to
// agree: when it did not, a reconnecting client with no filter was replayed
// nothing at all.
func TestBus_SinceWithNoTopicsMeansEveryTopic(t *testing.T) {
	bus, _ := newBus(t)
	bus.Publish(events.Event{Topic: events.TopicJob, Entity: "job:1"})
	bus.Publish(events.Event{Topic: events.TopicLibrary, Entity: "work:1"})

	subscription := bus.Subscribe(nil, 4)
	defer subscription.Close()

	missed, complete := bus.Since(0, subscription.Topics())
	if !complete {
		t.Error("complete = false with everything in the ring")
	}
	if len(missed) != 2 {
		t.Errorf("replayed %d events, want both", len(missed))
	}
}
