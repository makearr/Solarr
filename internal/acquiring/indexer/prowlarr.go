package indexer

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Priority is Prowlarr's own, 1 to 50, lower preferred: the scale Solarr uses.
	Priority int64
	// Supported reports a category of a media type Solarr serves (movies, audio, TV, books, adult).
	Supported bool
	// Tags link the indexer to the proxies of Prowlarr that carry the same tag.
	Tags []int64
}

type prowlarrIndexer struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Protocol     string  `json:"protocol"`
	Enable       bool    `json:"enable"`
	Priority     int64   `json:"priority"`
	Tags         []int64 `json:"tags"`
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
	body, err := httpx.ReadLimited(response.Body, maxProwlarrResponseBytes)
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
		supported := false
		for _, category := range entry.Capabilities.Categories {
			supported = supported || SupportedCategory(int(category.ID))
		}
		indexers = append(indexers, ProwlarrIndexer{
			ProwlarrID: entry.ID, Name: entry.Name, Impl: impl, Enabled: entry.Enable, Music: music,
			BaseURL:  base + "/" + strconv.FormatInt(entry.ID, 10),
			Priority: min(max(entry.Priority, 1), 50), Supported: supported, Tags: entry.Tags,
		})
	}
	return indexers, nil
}

// SupportedCategory reports whether a Newznab category belongs to a media type Solarr serves: movies (2000), audio (3000),
// TV (5000), adult (6000), books (7000) and other (8000), but not console (1000) or PC (4000).
func SupportedCategory(id int) bool { return id >= 2000 && id < 9000 && id/1000 != 4 }

// ProwlarrProxy is one indexer proxy of a Prowlarr: FlareSolverr, or an HTTP or SOCKS5 proxy without credentials.
type ProwlarrProxy struct {
	ProwlarrID int64
	Name       string
	// Kind is "flaresolverr", "http" or "socks5".
	Kind string
	URL  string
	Tags []int64
}

type prowlarrProxy struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Implementation string  `json:"implementation"`
	Tags           []int64 `json:"tags"`
	Fields         []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"fields"`
}

// ListProwlarrProxies answers the proxies a Prowlarr applies to indexers by tag. A proxy that needs credentials is left
// out: Prowlarr answers its password masked, so it cannot be copied.
func ListProwlarrProxies(ctx context.Context, client *httpx.Client, baseURL, apiKey string) ([]ProwlarrProxy, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/v1/indexerProxy", nil)
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr proxies: %w", err)
	}
	request.Header.Set("X-Api-Key", apiKey)
	response, err := client.Do(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr proxies: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := httpx.ReadLimited(response.Body, maxProwlarrResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("indexer: prowlarr proxies: read: %w", err)
	}
	var listed []prowlarrProxy
	if err := json.Unmarshal(body, &listed); err != nil {
		return nil, fmt.Errorf("indexer: prowlarr proxies: not a proxy list: %w", err)
	}
	proxies := make([]ProwlarrProxy, 0, len(listed))
	for _, entry := range listed {
		fields := map[string]string{}
		for _, field := range entry.Fields {
			fields[field.Name] = strings.TrimSpace(fmt.Sprint(field.Value))
		}
		proxy := ProwlarrProxy{ProwlarrID: entry.ID, Name: entry.Name, Tags: entry.Tags}
		switch strings.ToLower(entry.Implementation) {
		case "flaresolverr":
			proxy.Kind, proxy.URL = "flaresolverr", fields["host"]
		case "http", "socks5":
			if fields["username"] != "" {
				continue
			}
			proxy.Kind = strings.ToLower(entry.Implementation)
			proxy.URL = proxy.Kind + "://" + fields["host"] + ":" + fields["port"]
		default:
			continue
		}
		if proxy.URL != "" {
			proxies = append(proxies, proxy)
		}
	}
	return proxies, nil
}
