// Package animeids reads the Fribb anime-lists id map (one daily JSON file on GitHub): which AniDB, AniList and MyAnimeList
// entries belong to a TheTVDB series, so an anime's sources are asked by id instead of searched for.
package animeids

import (
	"context"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	listURL          = "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-full.json"
	maxResponseBytes = 32 << 20
	// refreshAfter is how long a read of the file is kept; the file changes about daily.
	refreshAfter = 24 * time.Hour
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Links are the ids of one series on the anime registries, in the file's order.
type Links struct {
	AniDB, MAL, AniList []int64
}

// entry is one line of the file; only what the map needs is read.
type entry struct {
	Type    string `json:"type"`
	AniDB   int64  `json:"anidb_id"`
	AniList int64  `json:"anilist_id"`
	MAL     int64  `json:"mal_id"`
	TVDB    int64  `json:"tvdb_id"`
}

// Map is the id map, read on first use and again when a day old.
type Map struct {
	http  Getter
	clock clock.Clock
	url   string

	mu     sync.Mutex
	loaded time.Time
	byTVDB map[int64]Links
}

// New builds a map over an httpx client carrying its own rate bucket.
func New(client Getter, clk clock.Clock) *Map {
	return &Map{http: client, clock: clk, url: listURL}
}

// ByTVDB answers the anime registry ids of a TheTVDB series; false when the file lists none. A failed read keeps the older copy.
func (m *Map) ByTVDB(ctx context.Context, tvdb int64) (Links, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failure error
	if m.byTVDB == nil || m.clock.Now().Sub(m.loaded) > refreshAfter {
		if failure = m.load(ctx); failure != nil && m.byTVDB != nil {
			m.loaded = m.clock.Now().Add(time.Hour - refreshAfter)
		}
	}
	links, found := m.byTVDB[tvdb]
	return links, found, failure
}

func (m *Map) load(ctx context.Context) error {
	var entries []entry
	if err := m.http.GetJSON(ctx, m.url, maxResponseBytes, &entries); err != nil {
		return err
	}
	byTVDB := make(map[int64]Links, len(entries)/4)
	for _, one := range entries {
		// OVAs, specials and films hang off a series in the file; only its series proper steer a lookup.
		if one.TVDB == 0 || (one.Type != "TV" && one.Type != "ONA") {
			continue
		}
		links := byTVDB[one.TVDB]
		links.AniDB = appendID(links.AniDB, one.AniDB)
		links.MAL = appendID(links.MAL, one.MAL)
		links.AniList = appendID(links.AniList, one.AniList)
		byTVDB[one.TVDB] = links
	}
	m.byTVDB, m.loaded = byTVDB, m.clock.Now()
	return nil
}

func appendID(ids []int64, id int64) []int64 {
	if id == 0 {
		return ids
	}
	for _, held := range ids {
		if held == id {
			return ids
		}
	}
	return append(ids, id)
}
