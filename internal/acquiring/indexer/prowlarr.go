package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxProwlarrResponseBytes bounds Prowlarr's indexer list; a few hundred indexers fit in well under it.
const maxProwlarrResponseBytes = 8 << 20

// ProwlarrIndexer is one indexer Prowlarr serves, and how Solarr reaches it through Prowlarr.
type ProwlarrIndexer struct {
	ProwlarrID int64
	Name       string
	// Impl is "newznab" for a usenet indexer, "torznab" for a torrent one.
	Impl    string
	Enabled bool
	// Music reports a category in the 3000 (Audio) range: an indexer that can find albums.
	Music bool
	// BaseURL is Prowlarr's own per-indexer endpoint, the one the *Arrs are given.
	BaseURL string
}

type prowlarrIndexer struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	Enable       bool   `json:"enable"`
	Capabilities struct {
		Categories []struct {
			ID int64 `json:"id"`
		} `json:"categories"`
	} `json:"capabilities"`
}

// ListProwlarr answers every indexer the Prowlarr at baseURL serves, as Lidarr's Prowlarr sync sees
// them: each reachable through Prowlarr with Prowlarr's own API key.
func ListProwlarr(ctx context.Context, client *httpx.Client, baseURL, apiKey string) ([]ProwlarrIndexer, error) {
	base := strings.TrimRight(baseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/indexer", nil)
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr: %w", err)
	}
	request.Header.Set("X-Api-Key", apiKey)
	response, err := client.Do(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProwlarrResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr: read: %w", err)
	}
	var listed []prowlarrIndexer
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("indexer: prowlarr: not a Prowlarr indexer list: %w", err)
	}

	indexers := make([]ProwlarrIndexer, 0, len(listed))
	for _, entry := range listed {
		impl := "newznab"
		if entry.Protocol == "torrent" {
			impl = "torznab"
		}
		music := false
		for _, category := range entry.Capabilities.Categories {
			if category.ID >= 3000 && category.ID < 4000 {
				music = true
				break
			}
		}
		indexers = append(indexers, ProwlarrIndexer{
			ProwlarrID: entry.ID, Name: entry.Name, Impl: impl, Enabled: entry.Enable, Music: music,
			BaseURL: base + "/" + strconv.FormatInt(entry.ID, 10),
		})
	}
	return indexers, nil
}
