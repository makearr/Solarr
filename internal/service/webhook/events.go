// Package webhook delivers Solarr's events to the addresses an administrator names: an outbox, a signed request, a
// backing-off retry. Presets shape the body for chat and push services.
package webhook

// Event names what happened; the set is closed and documented in documentation/webhooks.md.
type Event string

// The events a webhook may subscribe to.
const (
	EventGrabbed         Event = "grabbed"
	EventImported        Event = "imported"
	EventUpgraded        Event = "upgraded"
	EventImportFailed    Event = "import_failed"
	EventDownloadFailed  Event = "download_failed"
	EventRenamed         Event = "renamed"
	EventFileRecycled    Event = "file_recycled"
	EventWorkAdded       Event = "work_added"
	EventWorkRemoved     Event = "work_removed"
	EventRequestCreated  Event = "request_created"
	EventRequestApproved Event = "request_approved"
	EventHealthRaised    Event = "health_raised"
	EventHealthCleared   Event = "health_cleared"
	EventTest            Event = "test"
)

// Events lists the vocabulary in the order a person reads it.
var Events = []Event{
	EventGrabbed, EventImported, EventUpgraded, EventImportFailed, EventDownloadFailed, EventRenamed, EventFileRecycled,
	EventWorkAdded, EventWorkRemoved, EventRequestCreated, EventRequestApproved, EventHealthRaised, EventHealthCleared, EventTest,
}

// Known says the name is in the vocabulary.
func Known(name string) bool {
	for _, event := range Events {
		if string(event) == name {
			return true
		}
	}
	return false
}
