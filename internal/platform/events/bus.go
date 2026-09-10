package events

import (
	"sync"
	"sync/atomic"

	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// Bounds.
const (
	// DefaultSubscriberBuffer is how far behind one subscriber may fall before
	// its events start being dropped.
	DefaultSubscriberBuffer = 256

	// DefaultReplayBuffer is how many recent events are kept for a reconnecting client.
	DefaultReplayBuffer = 512
)

// Bus fans events out to subscribers.
type Bus struct {
	clock clock.Clock

	mu          sync.RWMutex
	subscribers map[uint64]*Subscription
	nextSub     uint64

	// nextID is monotonic across the process lifetime, so a client can compare
	// ids without knowing anything about the bus.
	nextID atomic.Uint64

	// replay is a ring of recent events for reconnecting clients.
	replayMu sync.RWMutex
	replay   []Event
	replayAt int

	dropped atomic.Int64
}

// NewBus builds a bus.
func NewBus(c clock.Clock) *Bus {
	return &Bus{
		clock:       c,
		subscribers: make(map[uint64]*Subscription),
		replay:      make([]Event, DefaultReplayBuffer),
	}
}

// Publish delivers an event to every interested subscriber.
func (b *Bus) Publish(event Event) {
	event.ID = b.nextID.Add(1)
	if event.At.IsZero() {
		event.At = b.clock.Now()
	}
	b.remember(event)

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, subscriber := range b.subscribers {
		if !subscriber.wants(event.Topic) {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			// Dropped, counted, and the subscriber is marked so it can tell its
			// client to resynchronise rather than quietly showing stale data.
			subscriber.missed.Store(true)
			b.dropped.Add(1)
		}
	}
}

// Dropped reports how many events have been dropped, for the
// mnx_events_dropped_total counter (ADR-9).
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

// LastID reports the most recently assigned event id.
func (b *Bus) LastID() uint64 { return b.nextID.Load() }

func (b *Bus) remember(event Event) {
	b.replayMu.Lock()
	defer b.replayMu.Unlock()
	b.replay[b.replayAt] = event
	b.replayAt = (b.replayAt + 1) % len(b.replay)
}

// Since returns events newer than the given id, and whether the history is complete (ADR-9).
func (b *Bus) Since(id uint64, topics map[Topic]bool) (found []Event, complete bool) {
	b.replayMu.RLock()
	defer b.replayMu.RUnlock()

	complete = true
	for offset := range b.replay {
		// Walk oldest to newest, starting at the write cursor.
		event := b.replay[(b.replayAt+offset)%len(b.replay)]
		if event.ID == 0 || event.ID <= id {
			continue
		}
		// An empty set means every topic, matching Subscription.wants.
		if len(topics) > 0 && !topics[event.Topic] {
			continue
		}
		found = append(found, event)
	}

	// The oldest event still held is newer than what the client last saw, so
	// whatever fell out of the ring in between is gone.
	if len(found) > 0 && found[0].ID > id+1 {
		complete = false
	}
	return found, complete
}

// Subscribe opens a subscription to the given topics. The caller must Close it,
// or the bus holds the buffer for the life of the process.
func (b *Bus) Subscribe(topics []Topic, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = DefaultSubscriberBuffer
	}
	wanted := make(map[Topic]bool, len(topics))
	for _, topic := range topics {
		wanted[topic] = true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSub++
	subscription := &Subscription{
		id:     b.nextSub,
		bus:    b,
		topics: wanted,
		events: make(chan Event, buffer),
	}
	b.subscribers[subscription.id] = subscription
	return subscription
}

// Subscribers reports how many are attached, for metrics and tests.
func (b *Bus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Subscription is one consumer's view of the bus.
type Subscription struct {
	id     uint64
	bus    *Bus
	topics map[Topic]bool
	events chan Event
	missed atomic.Bool
	closed sync.Once
}

// Events is the stream. It is closed when the subscription is.
func (s *Subscription) Events() <-chan Event { return s.events }

// Missed reports whether anything was dropped for this subscriber, so it can
// tell its client to resynchronise. Reading it clears the flag.
func (s *Subscription) Missed() bool { return s.missed.Swap(false) }

// Topics reports what this subscription listens for.
func (s *Subscription) Topics() map[Topic]bool { return s.topics }

func (s *Subscription) wants(topic Topic) bool {
	// An empty topic set means everything, which is what a client asking for no
	// particular topic expects.
	return len(s.topics) == 0 || s.topics[topic]
}

// Close detaches the subscription.
func (s *Subscription) Close() {
	s.closed.Do(func() {
		s.bus.mu.Lock()
		delete(s.bus.subscribers, s.id)
		s.bus.mu.Unlock()
		close(s.events)
	})
}
