// Package wikidata resolves a person's biography from their Wikidata QID
// (ADR-8's cross-provider identity bridge -- musicbrainz.Artist already
// extracts one from MusicBrainz's own "wikidata" URL relation, so this
// package never has to search for one itself). MusicBrainz carries no prose
// biography field at all; Wikidata's own short description plus the linked
// Wikipedia article's summary extract are the closest real substitute.
package wikidata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxResponseBytes bounds one lookup. A Wikipedia summary extract is a few
// paragraphs at most; a provider answering more than this is a reason to stop
// reading, not a reason to buffer it all first.
const maxResponseBytes = 1 << 20

// Biography is what this package can answer for one Wikidata QID.
type Biography struct {
	// Description is Wikidata's own short, one-line description ("English
	// singer, pianist and songwriter (born 1958)") -- present far more often
	// than a full extract.
	Description string
	// Extract is the linked Wikipedia article's summary, real prose, empty
	// when the entity has no English Wikipedia sitelink at all.
	Extract string
}

// entitiesResponse is Wikidata's wbgetentities action API shape (verified
// against a real live response, en.wikipedia.org's own convention of nesting
// language-keyed maps).
type entitiesResponse struct {
	Entities map[string]struct {
		// Missing is present (even as an empty string) when a well-formed QID
		// simply does not exist -- confirmed against a real live response,
		// distinct from the malformed-QID case, which answers a top-level
		// "error" object instead of an "entities" map at all.
		Missing      *string `json:"missing"`
		Descriptions map[string]struct {
			Value string `json:"value"`
		} `json:"descriptions"`
		Sitelinks map[string]struct {
			Title string `json:"title"`
		} `json:"sitelinks"`
	} `json:"entities"`
}

// summaryResponse is Wikipedia's REST summary endpoint shape.
type summaryResponse struct {
	Extract string `json:"extract"`
}

// Fetcher is what Biography needs from the two APIs it composes -- named so a
// caller can see the two-hop shape without reading the implementation, and so
// a test can fake either hop independently.
type Fetcher struct {
	wikidataClient  *httpx.Client
	wikipediaClient *httpx.Client
}

// Config is what a Fetcher needs. Two clients, not one: musicbrainz.org's own
// precedent of one httpx.Client (and ADR-13 rate-limit/breaker bucket) per
// distinct host, since www.wikidata.org and en.wikipedia.org are answered by
// entirely separate services with no shared budget.
type Config struct {
	WikidataClient  *httpx.Client
	WikipediaClient *httpx.Client
}

// New builds a Fetcher.
func New(config Config) Fetcher {
	return Fetcher{wikidataClient: config.WikidataClient, wikipediaClient: config.WikipediaClient}
}

// Biography answers qid's description and Wikipedia extract. A QID with no
// English Wikipedia sitelink still answers its description with Extract
// empty -- not an error, since a description alone is still worth having.
func (f Fetcher) Biography(ctx context.Context, qid string) (Biography, error) {
	query := url.Values{
		"action": {"wbgetentities"}, "ids": {qid}, "props": {"descriptions|sitelinks"},
		"languages": {"en"}, "format": {"json"},
	}
	entitiesURL := "https://www.wikidata.org/w/api.php?" + query.Encode()
	var entities entitiesResponse
	if err := getJSON(ctx, f.wikidataClient, entitiesURL, &entities); err != nil {
		return Biography{}, fmt.Errorf("wikidata: entity %s: %w", qid, err)
	}

	entity, ok := entities.Entities[qid]
	if !ok || entity.Missing != nil {
		return Biography{}, fmt.Errorf("wikidata: entity %s not found", qid)
	}

	biography := Biography{Description: entity.Descriptions["en"].Value}

	sitelink, ok := entity.Sitelinks["enwiki"]
	if !ok || sitelink.Title == "" {
		return biography, nil
	}

	summaryURL := "https://en.wikipedia.org/api/rest_v1/page/summary/" + pathEscape(sitelink.Title)
	var summary summaryResponse
	if err := getJSON(ctx, f.wikipediaClient, summaryURL, &summary); err != nil {
		// The description alone is still a real answer -- a Wikipedia hiccup
		// should not discard it.
		return biography, nil //nolint:nilerr
	}
	biography.Extract = summary.Extract
	return biography, nil
}

// pathEscape turns a Wikipedia page title into its URL path segment: spaces
// become underscores, MediaWiki's own convention, before the usual escaping.
func pathEscape(title string) string {
	return url.PathEscape(strings.ReplaceAll(title, " ", "_"))
}

func getJSON(ctx context.Context, client *httpx.Client, url string, into any) error {
	response, err := client.Get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
