package sabnzbd

import (
	"context"
	"fmt"
	"net/url"
)

// AddURLRequest is one NZB submission (ADR-57's own acquire.send). name is
// the NZB's own download URL, not a display name -- SABnzbd fetches it
// itself rather than MediaNexus downloading the artefact first.
type AddURLRequest struct {
	NZBURL   string
	Category string
	// Priority follows SABnzbd's own scale: -2 paused, -1 low, 0 normal,
	// 1 high, 2 force. Zero (Normal) is the useful default, so this is not
	// a pointer -- a caller wanting Paused must say so explicitly.
	Priority int
}

// addURLResponse mirrors SABnzbd's own mode=addurl JSON shape.
type addURLResponse struct {
	Status bool     `json:"status"`
	NzoIDs []string `json:"nzo_ids"`
	Error  string   `json:"error"`
}

// AddURL submits one NZB by URL -- this is the dispatch action ADR-57's own
// acquire.send performs, and the one call in this package with a real,
// irreversible side effect on the configured downloader. It exists so the
// candidate pipeline has somewhere to call once it is wired up; nothing in
// this codebase calls it yet, and it must never be exercised against a real
// downloader without the operator's own explicit go-ahead for that specific
// call (this project's own standing instruction, not a technical
// limitation here).
func (c Client) AddURL(ctx context.Context, request AddURLRequest) (clientRef string, err error) {
	params := url.Values{}
	params.Set("name", request.NZBURL)
	if request.Category != "" {
		params.Set("cat", request.Category)
	}
	if request.Priority != 0 {
		params.Set("priority", fmt.Sprintf("%d", request.Priority))
	}

	var parsed addURLResponse
	requestURL := c.apiURL("addurl", params)
	if err := c.getJSON(ctx, requestURL, &parsed); err != nil {
		return "", fmt.Errorf("sabnzbd: add url: %w", err)
	}
	if !parsed.Status {
		message := parsed.Error
		if message == "" {
			message = "SABnzbd refused the request"
		}
		return "", fmt.Errorf("sabnzbd: add url: %s", message)
	}
	if len(parsed.NzoIDs) == 0 {
		return "", fmt.Errorf("sabnzbd: add url: accepted but returned no nzo_id")
	}
	return parsed.NzoIDs[0], nil
}
