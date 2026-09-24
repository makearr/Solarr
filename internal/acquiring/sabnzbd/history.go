package sabnzbd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// HistorySlot is one finished download: completed, failed, or still post-processing.
type HistorySlot struct {
	ClientRef string
	Name      string
	Category  string
	// Status is SABnzbd's own: Completed, Failed, or a post-processing stage (Extracting, ...).
	Status      string
	StoragePath string
	FailMessage string
	SizeBytes   int64
	CompletedAt time.Time
}

type historyResponse struct {
	History struct {
		Slots []historySlot `json:"slots"`
	} `json:"history"`
}

type historySlot struct {
	NzoID       string `json:"nzo_id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Storage     string `json:"storage"`
	FailMessage string `json:"fail_message"`
	Bytes       int64  `json:"bytes"`
	Completed   int64  `json:"completed"`
}

// History answers the most recent finished downloads in category ("" for all).
func (c Client) History(ctx context.Context, category string, limit int) ([]HistorySlot, error) {
	params := url.Values{}
	if category != "" {
		params.Set("category", category)
	}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	var parsed historyResponse
	if err := c.getJSON(ctx, c.apiURL("history", params), &parsed); err != nil {
		return nil, fmt.Errorf("sabnzbd: history: %w", err)
	}
	slots := make([]HistorySlot, len(parsed.History.Slots))
	for i, slot := range parsed.History.Slots {
		slots[i] = HistorySlot{
			ClientRef: slot.NzoID, Name: slot.Name, Category: slot.Category, Status: slot.Status,
			StoragePath: slot.Storage, FailMessage: slot.FailMessage, SizeBytes: slot.Bytes,
			CompletedAt: time.Unix(slot.Completed, 0),
		}
	}
	return slots, nil
}

type statusResponse struct {
	Status bool   `json:"status"`
	Error  string `json:"error"`
}

// Remove deletes one download from the queue or the history; deleteFiles also removes what it
// downloaded. Only ever called for a download Solarr itself sent (section 10.9).
func (c Client) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	params := url.Values{}
	params.Set("name", "delete")
	params.Set("value", clientRef)
	if deleteFiles {
		params.Set("del_files", "1")
	}
	for _, mode := range []string{"queue", "history"} {
		var parsed statusResponse
		if err := c.getJSON(ctx, c.apiURL(mode, params), &parsed); err != nil {
			return fmt.Errorf("sabnzbd: remove from %s: %w", mode, err)
		}
		if !parsed.Status && parsed.Error != "" {
			return fmt.Errorf("sabnzbd: remove from %s: %s", mode, parsed.Error)
		}
	}
	return nil
}
