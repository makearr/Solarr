package sabnzbd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// AddFileRequest is one NZB uploaded to SABnzbd (ADR-57's acquire.send).
type AddFileRequest struct {
	// Name is the job's name in SABnzbd: the release title, never a URL.
	Name     string
	NZB      []byte
	Category string
	// Priority follows SABnzbd's scale: -2 paused, -1 low, 0 normal, 1 high, 2 force.
	Priority int
}

type addResponse struct {
	Status bool     `json:"status"`
	NzoIDs []string `json:"nzo_ids"`
	Error  string   `json:"error"`
}

// AddFile uploads the NZB Solarr fetched itself, as Lidarr does: SABnzbd never has to reach the
// indexer, and the job is named after the release from the start.
func (c Client) AddFile(ctx context.Context, request AddFileRequest) (clientRef string, err error) {
	params := url.Values{}
	params.Set("nzbname", request.Name)
	if request.Category != "" {
		params.Set("cat", request.Category)
	}
	if request.Priority != 0 {
		params.Set("priority", fmt.Sprintf("%d", request.Priority))
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("name", request.Name+".nzb")
	if err != nil {
		return "", fmt.Errorf("sabnzbd: add file: %w", err)
	}
	if _, err := part.Write(request.NZB); err != nil {
		return "", fmt.Errorf("sabnzbd: add file: %w", err)
	}
	if err := form.Close(); err != nil {
		return "", fmt.Errorf("sabnzbd: add file: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("addfile", params), &body)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: add file: %w", err)
	}
	httpRequest.Header.Set("Content-Type", form.FormDataContentType())
	response, err := c.http.Do(ctx, httpRequest)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: add file: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	var parsed addResponse
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("sabnzbd: add file: read response: %w", err)
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("sabnzbd: add file: decode response: %w", err)
	}
	if !parsed.Status {
		if parsed.Error == "" {
			return "", fmt.Errorf("sabnzbd: add file: %w (SABnzbd gave no reason; often it has downloaded it before)",
				download.ErrRefused)
		}
		return "", fmt.Errorf("sabnzbd: add file: %w: %s", download.ErrRefused, parsed.Error)
	}
	if len(parsed.NzoIDs) == 0 {
		return "", fmt.Errorf("sabnzbd: add file: accepted but returned no nzo_id")
	}
	return parsed.NzoIDs[0], nil
}
