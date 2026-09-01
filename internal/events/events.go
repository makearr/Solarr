// Package events is the notification bus. Events are lossy by design (ADR-9,
// Invariant 24): anything that must happen is a job, and the bus only tells
// interested parties that something already happened.
//
// That is what allows every buffer here to be bounded and to drop rather than
// block (Invariant 25). A bus that blocks when a subscriber is slow makes the
// publisher's correctness depend on a consumer's speed, and a bus that grows
// instead turns a slow browser tab into an out-of-memory kill.
package events

import "time"

// Topic groups events so a subscriber can ask for only what it renders
// (architecture section 13.4).
type Topic string

// The topics of section 13.4.
const (
	TopicJob          Topic = "job"
	TopicAcquisition  Topic = "acquisition"
	TopicLibrary      Topic = "library"
	TopicNotification Topic = "notification"
	TopicPressure     Topic = "pressure"
)

// Known lists every topic, so an unknown one in a subscription can be rejected
// rather than silently matching nothing.
func Known() []Topic {
	return []Topic{TopicJob, TopicAcquisition, TopicLibrary, TopicNotification, TopicPressure}
}

// String names the topic.
func (t Topic) String() string { return string(t) }

// Valid reports whether a topic exists.
func (t Topic) Valid() bool {
	for _, known := range Known() {
		if t == known {
			return true
		}
	}
	return false
}

// Event is one thing that happened.
type Event struct {
	Topic Topic `json:"topic"`

	// Entity identifies what changed, as "job:42" or "work:1183". It is the
	// coalescing key: within one flush window, repeated changes to the same
	// entity collapse to the latest, so a bulk import emits one message per
	// entity per tick rather than a storm (section 13.4).
	Entity string `json:"entity"`

	// CategoryID scopes visibility. Zero means the event belongs to no
	// category -- a backup finishing, disk pressure -- and is filtered by role
	// rather than by category access (ADR-27).
	CategoryID int64 `json:"category_id,omitempty"`

	// Kind is what happened to the entity: "created", "updated", "deleted".
	Kind string `json:"kind"`

	// Data is the payload the UI renders. It must never carry a secret, and
	// must never be the only record of anything: a dropped event has to be
	// recoverable by refetching (ADR-9).
	Data any `json:"data,omitempty"`

	// At is when it happened, for ordering in a UI that renders out of order.
	At time.Time `json:"at"`

	// ID is assigned by the bus, monotonically, so a reconnecting client can
	// say what it last saw.
	ID uint64 `json:"id"`
}
