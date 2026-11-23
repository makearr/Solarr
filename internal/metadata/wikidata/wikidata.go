// Package wikidata resolves a person's biography from their Wikidata QID (ADR-8; musicbrainz.Artist already extracts it): MusicBrainz has no prose biography, so Wikidata's short description plus the linked Wikipedia summary substitute.
package wikidata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// maxResponseBytes bounds one lookup: a Wikipedia summary is short but wbgetentities claims are unbounded per entity (props=claims was added for P18), so the cap was raised from 1 MiB, generous but bounded as arrimport's cap learned (todo 4.13).
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
	// ImageURL is a fetchable URL for the entity's P18 image claim, empty when none; built via Special:FilePath, which 302s to the file bytes, instead of a second API call.
	ImageURL string
}

// entitiesResponse is Wikidata's wbgetentities action API shape (verified
// against a real live response, en.wikipedia.org's own convention of nesting
// language-keyed maps).
type entitiesResponse struct {
	Entities map[string]struct {
		// Missing is present (even empty) when a well-formed QID does not exist, unlike a malformed QID, which answers a top-level "error" object instead of an "entities" map.
		Missing      *string `json:"missing"`
		Descriptions map[string]struct {
			Value string `json:"value"`
		} `json:"descriptions"`
		Sitelinks map[string]struct {
			Title string `json:"title"`
		} `json:"sitelinks"`
		// Claims carries P18 among many properties never read, kept raw because values are strings, objects or numbers by property, and decoding them all as strings failed every real artist (occupation P106 is an object, found 2026-09-17).
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

// imageURLFor builds the file URL from a P18 Commons file name, reusing pathEscape's space-to-underscore convention, which is MediaWiki title normalisation and works for Special:FilePath.
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

// sitelinkTitle is a Wikipedia article: the language edition it is in and its title there.
type sitelinkTitle struct{ language, title string }

// Fetcher is what Biography needs from the two APIs it composes -- named so a
// caller can see the two-hop shape without reading the implementation, and so
// a test can fake either hop independently.
type Fetcher struct {
	wikidataClient  *httpx.Client
	wikipediaClient *httpx.Client
	clock           clock.Clock
	language        func() string
}

// Config is what a Fetcher needs: two clients, one per host (and ADR-13 bucket) as musicbrainz does, since www.wikidata.org and en.wikipedia.org are separate services with no shared budget.
type Config struct {
	WikidataClient  *httpx.Client
	WikipediaClient *httpx.Client
	// Clock paces the retries of an overloaded query service; nil is the system clock.
	Clock clock.Clock
	// Language names the metadata language on each call, a two-letter code; nil or empty is English.
	Language func() string
}

// New builds a Fetcher.
func New(config Config) Fetcher {
	paced := config.Clock
	if paced == nil {
		paced = clock.NewSystem()
	}
	return Fetcher{wikidataClient: config.WikidataClient, wikipediaClient: config.WikipediaClient, clock: paced, language: config.Language}
}

// tongue is the Wikipedia language the next read asks in; English is the fallback every read keeps.
func (f Fetcher) tongue() string {
	if f.language != nil {
		if code := f.language(); code != "" {
			return code
		}
	}
	return "en"
}

// maxEntitiesPerRequest is wbgetentities' own limit on ids in one call.
const maxEntitiesPerRequest = 50

// summaryWorkers overlaps Wikipedia summaries; its REST API allows far more than the client paces.
const summaryWorkers = 8

// Biography answers qid's description and Wikipedia extract. A QID with no
// English Wikipedia sitelink still answers its description with Extract
// empty -- not an error, since a description alone is still worth having.
func (f Fetcher) Biography(ctx context.Context, qid string) (Biography, error) {
	biographies, err := f.Biographies(ctx, []string{qid})
	if err != nil {
		return Biography{}, err
	}
	biography, ok := biographies[qid]
	if !ok {
		return Biography{}, fmt.Errorf("wikidata: entity %s not found", qid)
	}
	return biography, nil
}

// Biographies answers many QIDs with one Wikidata request per maxEntitiesPerRequest: Wikimedia
// asks for serial requests, and batching is how it wants volume. A QID that does not exist is
// absent from the answer; a Wikipedia summary that fails leaves that Extract empty.
func (f Fetcher) Biographies(ctx context.Context, qids []string) (map[string]Biography, error) {
	unique := make([]string, 0, len(qids))
	seen := make(map[string]bool, len(qids))
	for _, qid := range qids {
		if qid != "" && !seen[qid] {
			seen[qid] = true
			unique = append(unique, qid)
		}
	}
	biographies := make(map[string]Biography, len(unique))
	tongue := f.tongue()
	languages := "en"
	if tongue != "en" {
		languages = tongue + "|en"
	}
	titles := make(map[string]sitelinkTitle)
	for start := 0; start < len(unique); start += maxEntitiesPerRequest {
		chunk := unique[start:min(start+maxEntitiesPerRequest, len(unique))]
		query := url.Values{
			"action": {"wbgetentities"}, "ids": {strings.Join(chunk, "|")}, "props": {"descriptions|sitelinks|claims"},
			"languages": {languages}, "format": {"json"},
		}
		var entities entitiesResponse
		if err := getJSON(ctx, f.wikidataClient, "https://www.wikidata.org/w/api.php?"+query.Encode(), &entities); err != nil {
			return nil, fmt.Errorf("wikidata: entities %s: %w", strings.Join(chunk, ","), err)
		}
		for _, qid := range chunk {
			entity, ok := entities.Entities[qid]
			if !ok || entity.Missing != nil {
				continue
			}
			description := entity.Descriptions[tongue].Value
			if description == "" {
				description = entity.Descriptions["en"].Value
			}
			biographies[qid] = Biography{Description: description, ImageURL: imageURLFor(entity.Claims)}
			for _, wanted := range []string{tongue, "en"} {
				if sitelink, ok := entity.Sitelinks[wanted+"wiki"]; ok && sitelink.Title != "" {
					titles[qid] = sitelinkTitle{language: wanted, title: sitelink.Title}
					break
				}
			}
		}
	}

	titled := make([]string, 0, len(titles))
	for qid := range titles {
		titled = append(titled, qid)
	}
	found := make([]string, len(titled))
	// A Wikipedia hiccup keeps the description, which is still a real answer, so a failed summary is left out.
	failed := parallel.Do(ctx, len(titled), summaryWorkers, func(ctx context.Context, index int) error {
		var summary summaryResponse
		err := getJSON(ctx, f.wikipediaClient,
			"https://"+titles[titled[index]].language+".wikipedia.org/api/rest_v1/page/summary/"+pathEscape(titles[titled[index]].title), &summary)
		if err == nil {
			found[index] = summary.Extract
		}
		return err
	})
	for index, qid := range titled {
		if failed[index] != nil {
			continue
		}
		biography := biographies[qid]
		biography.Extract = found[index]
		biographies[qid] = biography
	}
	return biographies, nil
}

// pathEscape turns a Wikipedia page title into its URL path segment: spaces
// become underscores, MediaWiki's own convention, before the usual escaping.
func pathEscape(title string) string {
	return url.PathEscape(strings.ReplaceAll(title, " ", "_"))
}

// ErrResponseTooLarge reports a response past maxResponseBytes, refused honestly (read one byte over the limit) instead of truncated into a confusing decode error, as arrimport does.
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
