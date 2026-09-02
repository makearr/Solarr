// Package events is the notification bus (ADR-9, Invariant 24).
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

	// Entity identifies what changed, as "job:42" or "work:1183" (section 13.4).
	Entity string `json:"entity"`

	// CategoryID scopes visibility (ADR-27).
	CategoryID int64 `json:"category_id,omitempty"`

	// Kind is what happened to the entity: "created", "updated", "deleted".
	Kind string `json:"kind"`

	// Data is the payload the UI renders (ADR-9).
	Data any `json:"data,omitempty"`

	// At is when it happened, for ordering in a UI that renders out of order.
	At time.Time `json:"at"`

	// ID is assigned by the bus, monotonically, so a reconnecting client can
	// say what it last saw.
	ID uint64 `json:"id"`
}
