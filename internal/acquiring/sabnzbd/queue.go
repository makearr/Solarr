package sabnzbd

import (
	"context"
	"fmt"
	"strconv"
)

// QueueState is SABnzbd's own current queue -- ADR-12's queue and bandwidth
// pressure signals, polled rather than pushed (§9's planner and §10.6's
// resource gate are both future callers, not this package's own concern).
type QueueState struct {
	Paused bool
	// SizeBytes/SizeLeftBytes are the whole queue's own totals -- SABnzbd
	// reports both as decimal megabyte strings ("12583.5"), converted here
	// so nothing above this package touches SABnzbd's own units.
	SizeBytes        int64
	SizeLeftBytes    int64
	SpeedBytesPerSec int64
	SlotsTotal       int
	Slots            []QueueSlot
}

// QueueSlot is one item in the queue. ClientRef is SABnzbd's own nzo_id --
// ADR-46's own "acquisitions are tracked by client reference, never by
// path" (§10.9).
type QueueSlot struct {
	ClientRef         string
	Filename          string
	Category          string
	SizeBytes         int64
	SizeLeftBytes     int64
	PercentComplete   int
	Status            string
	TimeLeftFormatted string
}

// queueResponseXML mirrors SABnzbd's own mode=queue JSON shape (despite the
// name, this package speaks nothing but JSON -- named for the request mode,
// not the wire format). Only the fields this package reads are declared.
type queueResponse struct {
	Queue struct {
		Paused        bool        `json:"paused"`
		SizeMB        string      `json:"mb"`
		SizeLeftMB    string      `json:"mbleft"`
		SpeedKBPerSec string      `json:"kbpersec"`
		SlotsTotal    int         `json:"noofslots_total"`
		Slots         []queueSlot `json:"slots"`
	} `json:"queue"`
}

type queueSlot struct {
	NzoID      string `json:"nzo_id"`
	Filename   string `json:"filename"`
	Cat        string `json:"cat"`
	MB         string `json:"mb"`
	MBLeft     string `json:"mbleft"`
	Percentage string `json:"percentage"`
	Status     string `json:"status"`
	TimeLeft   string `json:"timeleft"`
}

// Queue fetches the current queue state.
func (c Client) Queue(ctx context.Context) (QueueState, error) {
	var parsed queueResponse
	requestURL := c.apiURL("queue", nil)
	if err := c.getJSON(ctx, requestURL, &parsed); err != nil {
		return QueueState{}, fmt.Errorf("sabnzbd: queue: %w", err)
	}

	slots := make([]QueueSlot, len(parsed.Queue.Slots))
	for i, slot := range parsed.Queue.Slots {
		percent, _ := strconv.Atoi(slot.Percentage)
		slots[i] = QueueSlot{
			ClientRef: slot.NzoID, Filename: slot.Filename, Category: slot.Cat,
			SizeBytes: megabytesToBytes(slot.MB), SizeLeftBytes: megabytesToBytes(slot.MBLeft),
			PercentComplete: percent, Status: slot.Status, TimeLeftFormatted: slot.TimeLeft,
		}
	}

	return QueueState{
		Paused:    parsed.Queue.Paused,
		SizeBytes: megabytesToBytes(parsed.Queue.SizeMB), SizeLeftBytes: megabytesToBytes(parsed.Queue.SizeLeftMB),
		SpeedBytesPerSec: kbPerSecToBytesPerSec(parsed.Queue.SpeedKBPerSec),
		SlotsTotal:       parsed.Queue.SlotsTotal, Slots: slots,
	}, nil
}
