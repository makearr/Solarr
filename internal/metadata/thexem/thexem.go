// Package thexem reads TheXEM (thexem.info), the community map from TheTVDB's episode numbers to the numbers scene
// releases carry (pain point 30, stage 8 T4). Keyless; answers are kept in metadata.db and a stale one outlives an outage.
package thexem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/metadata/metacache"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	baseURL          = "https://thexem.info"
	maxResponseBytes = 16 << 20
	// FreshFor is how long a cached answer is used without asking again; maps change rarely, Sonarr reads them twice a day.
	FreshFor = 12 * time.Hour
	source   = "thexem"
)

// Cache kinds in metadata.db.
const (
	kindHaveMap = "thexem:havemap"
	kindMap     = "thexem:map"
	kindNames   = "thexem:names"
)

// ErrRefused means TheXEM answered with a failure of its own rather than a map.
var ErrRefused = errors.New("thexem: refused")

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// Cache is the part of metacache.Cache this package needs.
type Cache interface {
	Get(ctx context.Context, kind, externalID string) (metacache.Entry, error)
	Set(ctx context.Context, kind, externalID string, entry metacache.Entry) error
}

// Number is one episode's place in one numbering; Absolute is zero when the numbering has none.
type Number struct{ Season, Episode, Absolute int }

// Mapping pairs an episode's TheTVDB number with the scene's.
type Mapping struct{ TVDB, Scene Number }

// Client asks TheXEM.
type Client struct {
	http  Doer
	cache Cache
	clock clock.Clock
}

// New builds a client over an httpx client carrying TheXEM's own rate bucket.
func New(client Doer, cache Cache, clk clock.Clock) Client {
	return Client{http: client, cache: cache, clock: clk}
}

// envelope is every TheXEM answer: result is "success" or "failure", data its payload.
type envelope struct {
	Result  string          `json:"result"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

// Mappings is a TheTVDB series' scene numbering, nil when TheXEM maps none of it.
func (c Client) Mappings(ctx context.Context, tvdbID int64) ([]Mapping, error) {
	mapped, err := c.mapped(ctx)
	if err != nil {
		return nil, err
	}
	if !mapped[tvdbID] {
		return nil, nil
	}
	id := strconv.FormatInt(tvdbID, 10)
	data, err := c.cached(ctx, kindMap, id, "/map/all?origin=tvdb&id="+id)
	if errors.Is(err, ErrRefused) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeMappings(data)
}

// Names are the scene names of a TheTVDB series that hold for every season.
func (c Client) Names(ctx context.Context, tvdbID int64) ([]string, error) {
	data, err := c.cached(ctx, kindNames, "tvdb", "/map/allNames?origin=tvdb&seasonNumbers=1")
	if errors.Is(err, ErrRefused) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	all, err := decodeNames(data)
	if err != nil {
		return nil, err
	}
	names := all[tvdbID]
	slices.Sort(names)
	return slices.Compact(names), nil
}

// Ping asks for the list of mapped series past the cache, for the connection test.
func (c Client) Ping(ctx context.Context) error {
	data, err := c.fetch(ctx, "/map/havemap?origin=tvdb")
	if err != nil {
		return err
	}
	_, err = decodeIDs(data)
	return err
}

// mapped is the set of TheTVDB series TheXEM holds a map for: one call saves a call for every series it does not.
func (c Client) mapped(ctx context.Context) (map[int64]bool, error) {
	data, err := c.cached(ctx, kindHaveMap, "tvdb", "/map/havemap?origin=tvdb")
	if err != nil {
		return nil, err
	}
	return decodeIDs(data)
}

// cached answers the data of a cached call while it is fresh, else asks; a failed ask falls back to what is cached.
func (c Client) cached(ctx context.Context, kind, id, path string) (json.RawMessage, error) {
	entry, cacheErr := c.cache.Get(ctx, kind, id)
	if cacheErr == nil && c.clock.Since(entry.FetchedAt) < FreshFor {
		return unwrap([]byte(entry.Body))
	}
	body, err := c.fetchBody(ctx, path)
	if err == nil {
		data, decodeErr := unwrap(body)
		// A refusal is an answer too: TheXEM saying it maps nothing is kept as long as a map would be.
		if decodeErr == nil || errors.Is(decodeErr, ErrRefused) {
			if setErr := c.cache.Set(ctx, kind, id, metacache.Entry{Source: source, Body: string(body), FetchedAt: c.clock.Now()}); setErr != nil {
				return nil, fmt.Errorf("thexem: cache %s: %w", kind, setErr)
			}
			return data, decodeErr
		}
		err = decodeErr
	}
	if cacheErr == nil {
		return unwrap([]byte(entry.Body))
	}
	return nil, err
}

func (c Client) fetch(ctx context.Context, path string) (json.RawMessage, error) {
	body, err := c.fetchBody(ctx, path)
	if err != nil {
		return nil, err
	}
	return unwrap(body)
}

func (c Client) fetchBody(ctx context.Context, path string) ([]byte, error) {
	response, err := c.http.Get(ctx, baseURL+path)
	if err != nil {
		return nil, fmt.Errorf("thexem: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("thexem: %s: status %d", path, response.StatusCode)
	}
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("thexem: %s: read: %w", path, err)
	}
	return body, nil
}

func unwrap(body []byte) (json.RawMessage, error) {
	var answer envelope
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("thexem: decode: %w", err)
	}
	if answer.Result != "success" {
		return nil, fmt.Errorf("%w: %s", ErrRefused, answer.Message)
	}
	return answer.Data, nil
}

// decodeIDs reads havemap's list, whose ids TheXEM writes as strings.
func decodeIDs(data json.RawMessage) (map[int64]bool, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("thexem: havemap: %w", err)
	}
	ids := make(map[int64]bool, len(raw))
	for _, entry := range raw {
		if id, ok := looseInt(entry); ok && id > 0 {
			ids[id] = true
		}
	}
	return ids, nil
}

type wireNumber struct {
	Season   int `json:"season"`
	Episode  int `json:"episode"`
	Absolute int `json:"absolute"`
}

// decodeMappings reads /map/all; the extra "scene_2"/"tvdb_2" halves of a split episode are left out.
func decodeMappings(data json.RawMessage) ([]Mapping, error) {
	var raw []struct {
		Scene *wireNumber `json:"scene"`
		TVDB  *wireNumber `json:"tvdb"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("thexem: map: %w", err)
	}
	mappings := make([]Mapping, 0, len(raw))
	for _, entry := range raw {
		if entry.Scene == nil || entry.TVDB == nil || entry.TVDB.Episode == 0 || entry.Scene.Episode == 0 {
			continue
		}
		mappings = append(mappings, Mapping{
			TVDB:  Number{Season: entry.TVDB.Season, Episode: entry.TVDB.Episode, Absolute: entry.TVDB.Absolute},
			Scene: Number{Season: entry.Scene.Season, Episode: entry.Scene.Episode, Absolute: entry.Scene.Absolute},
		})
	}
	return mappings, nil
}

// decodeNames reads /map/allNames: per TheTVDB id, each name either bare or as {name: season}, season -1 for all.
func decodeNames(data json.RawMessage) (map[int64][]string, error) {
	var raw map[string][]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("thexem: names: %w", err)
	}
	names := make(map[int64][]string, len(raw))
	for key, entries := range raw {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			var bare string
			if json.Unmarshal(entry, &bare) == nil {
				names[id] = appendName(names[id], bare)
				continue
			}
			var seasons map[string]json.RawMessage
			if json.Unmarshal(entry, &seasons) != nil {
				continue
			}
			for name, season := range seasons {
				if number, ok := looseInt(season); ok && number == -1 {
					names[id] = appendName(names[id], name)
				}
			}
		}
	}
	return names, nil
}

func appendName(names []string, name string) []string {
	if name = strings.TrimSpace(name); name == "" {
		return names
	}
	return append(names, name)
}

// looseInt reads a number TheXEM may write either bare or quoted.
func looseInt(raw json.RawMessage) (int64, bool) {
	text := string(bytes.Trim(bytes.TrimSpace(raw), `"`))
	number, err := strconv.ParseInt(text, 10, 64)
	return number, err == nil
}
