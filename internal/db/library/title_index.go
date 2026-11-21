package library

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

// titleIndexTTL is how long the titles of a category are held: a work added elsewhere in that time is simply not found by
// title, the provider is asked, and adding it finds the held work by id.
const titleIndexTTL = 30 * time.Second

type titleScope struct {
	mediaType  string
	categoryID int64
}

// TitleIndex answers "which works answer to one of these titles" from the titles of a category read once, instead of
// reading and normalising all of them for every file of a scan.
type TitleIndex struct {
	source interface {
		TitlesOf(ctx context.Context, mediaType string, categoryID int64) ([]SeriesTitle, error)
	}
	clock clock.Clock

	mu      sync.Mutex
	entries map[titleScope]*titleEntry
}

type titleEntry struct {
	mu        sync.Mutex // held while the entry is built, so two scan workers do not both read the category
	builtAt   time.Time
	built     bool
	byKey     map[string][]SeriesTitle
	canonical map[int64]SeriesTitle

	// all is every title of the category as read, for callers that normalise them their own way.
	all      []SeriesTitle
	allBuilt bool
	allAt    time.Time
}

// Lookup returns the held titles whose normalised form (key, one normaliser per media type) is one of wanted, with the
// canonical title of each work they belong to, ordered by work.
func (t *TitleIndex) Lookup(ctx context.Context, mediaType string, categoryID int64, key func(string) string, wanted []string) ([]SeriesTitle, error) {
	scope := titleScope{mediaType, categoryID}
	entry := t.entry(scope)

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.built || t.clock.Now().Sub(entry.builtAt) >= titleIndexTTL {
		titles, err := t.source.TitlesOf(ctx, mediaType, categoryID)
		if err != nil {
			return nil, err
		}
		entry.byKey, entry.canonical = make(map[string][]SeriesTitle), map[int64]SeriesTitle{}
		for _, title := range titles {
			normalised := key(title.Title)
			entry.byKey[normalised] = append(entry.byKey[normalised], title)
			if title.Canonical {
				entry.canonical[title.WorkID] = title
			}
		}
		entry.built, entry.builtAt = true, t.clock.Now()
	}

	var rows []SeriesTitle
	seen := map[SeriesTitle]bool{}
	add := func(title SeriesTitle) {
		if !seen[title] {
			seen[title] = true
			rows = append(rows, title)
		}
	}
	for _, name := range wanted {
		for _, title := range entry.byKey[name] {
			add(title)
			if canonical, ok := entry.canonical[title.WorkID]; ok {
				add(canonical)
			}
		}
	}
	slices.SortStableFunc(rows, func(a, b SeriesTitle) int {
		if a.WorkID != b.WorkID {
			return int(a.WorkID - b.WorkID)
		}
		if a.Canonical != b.Canonical {
			if a.Canonical {
				return -1
			}
			return 1
		}
		return 0
	})
	return rows, nil
}

func (t *TitleIndex) entry(scope titleScope) *titleEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = map[titleScope]*titleEntry{}
	}
	entry, held := t.entries[scope]
	if !held {
		entry = &titleEntry{}
		t.entries[scope] = entry
	}
	return entry
}

// All returns every title of a category, read at most once per TTL: what a caller that matches by its own normaliser
// asks for once per item.
func (t *TitleIndex) All(ctx context.Context, mediaType string, categoryID int64) ([]SeriesTitle, error) {
	entry := t.entry(titleScope{mediaType, categoryID})
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.allBuilt || t.clock.Now().Sub(entry.allAt) >= titleIndexTTL {
		titles, err := t.source.TitlesOf(ctx, mediaType, categoryID)
		if err != nil {
			return nil, err
		}
		entry.all, entry.allBuilt, entry.allAt = titles, true, t.clock.Now()
	}
	return entry.all, nil
}

// Invalidate drops the titles held for a category, after a work was added to it.
func (t *TitleIndex) Invalidate(mediaType string, categoryID int64) {
	t.mu.Lock()
	entry := t.entries[titleScope{mediaType, categoryID}]
	t.mu.Unlock()
	if entry == nil {
		return
	}
	entry.mu.Lock()
	entry.built, entry.allBuilt = false, false
	entry.mu.Unlock()
}
