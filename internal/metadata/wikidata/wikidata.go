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
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxResponseBytes bounds one lookup. A Wikipedia summary extract is a few
// paragraphs at most, but wbgetentities' own claims are unbounded per entity
// (props=claims was added for P18 alone; a prolific entity can carry
// hundreds of unrelated statements) -- raised from the original 1 MiB
// (description/sitelinks only) with the same "generous but still bounded"
// reasoning arrimport's own response cap learned the hard way (16 MiB
// silently truncated a real 28 MiB response, todo 4.13).
const maxResponseBytes = 8 << 20

// Biography is what this package can answer for one Wikidata QID.
type Biography struct {
	// Description is Wikidata's own short, one-line description ("English
	// singer, pianist and songwriter (born 1958)") -- present far more often
	// than a full extract.
	Description string
	// Extract is the linked Wikipedia article's summary, real prose, empty
	// when the entity has no English Wikipedia sitelink at all.
	Extract string
	// ImageURL is a direct, fetchable URL for the entity's own P18 ("image")
	// claim, empty when it has none -- the common case for anyone without a
	// Wikipedia-notable photo. Built from the Commons file name via
	// Special:FilePath rather than a second API call: that endpoint 302s
	// straight to the file's own bytes, which is all a caller here ever
	// wants (never the Commons page itself).
	ImageURL string
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
		// Claims carries P18 ("image") among many others this package never
		// reads, kept raw per property: their values are strings, objects or
		// numbers depending on the property, and decoding them all as strings
		// failed every real artist, whose occupation (P106) is an object
		// (found 2026-09-17).
		Claims map[string]json.RawMessage `json:"claims"`
	} `json:"entities"`
}

// wikidataImageProperty is P18, Wikidata's own "image" claim.
const wikidataImageProperty = "P18"

// commonsFilePathBase redirects straight to a Commons file's own bytes --
// MediaWiki's Special:FilePath, not the API, since this package wants an
// image to hand an httpx.Client, not Commons page metadata.
const commonsFilePathBase = "https://commons.wikimedia.org/wiki/Special:FilePath/"

// commonsImageWidth asks Commons for a rendition no wider than the largest
// artwork size (platform/artwork's Full). The original upload is often tens of
// megabytes, or a TIFF or SVG the artwork decoder cannot read at all.
const commonsImageWidth = "1000"

// imageURLFor builds the direct file URL from a P18 claim's own Commons file
// name, reusing pathEscape's own space-to-underscore convention (MediaWiki's
// own title normalisation, not just a Wikipedia-summary-path quirk --
// Special:FilePath/Vutu_Doci_1981.jpg redirects correctly live, verified
// while adding this).
func imageURLFor(claims map[string]json.RawMessage) string {
	raw, found := claims[wikidataImageProperty]
	if !found {
		return ""
	}
	var images []struct {
		Mainsnak struct {
			Datavalue struct {
				Value string `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	}
	if err := json.Unmarshal(raw, &images); err != nil || len(images) == 0 || images[0].Mainsnak.Datavalue.Value == "" {
		return ""
	}
	return commonsFilePathBase + pathEscape(images[0].Mainsnak.Datavalue.Value) + "?width=" + commonsImageWidth
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
		"action": {"wbgetentities"}, "ids": {qid}, "props": {"descriptions|sitelinks|claims"},
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

	biography := Biography{
		Description: entity.Descriptions["en"].Value,
		ImageURL:    imageURLFor(entity.Claims),
	}

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

// ErrResponseTooLarge reports a response this package refused to finish
// reading past maxResponseBytes -- arrimport.ErrResponseTooLarge's own
// reasoning: read one byte past the limit so an over-limit response is
// refused honestly instead of silently truncated into a confusing decode
// error with no hint why.
var ErrResponseTooLarge = errors.New("wikidata: response exceeded the size limit")

func getJSON(ctx context.Context, client *httpx.Client, url string, into any) error {
	response, err := client.Get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("%w (over %d bytes)", ErrResponseTooLarge, maxResponseBytes)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
