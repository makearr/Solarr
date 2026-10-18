package library

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// AutoCrossoverSource marks an event the library found itself, as opposed to one a person made.
const AutoCrossoverSource = "auto"

const (
	// eventWindow is how far apart the first and the last episode of one event may have aired: the halves of a
	// midseason event are weeks apart.
	eventWindow = 60 * 24 * time.Hour
	// eventMaxEpisodes keeps a shared episode title from becoming an event.
	eventMaxEpisodes = 12
)

var (
	partNumberSuffix = regexp.MustCompile(`(?i)\s*[:,\-\x{2013}]?\s*[(\[]\s*(\d{1,2}|[ivx]{1,4})\s*[)\]]\s*$`)
	partWordSuffix   = regexp.MustCompile(`(?i)\s*[:,\-\x{2013}]?\s*\(?(?:part|pt\.?)\s*(\w+)\)?\s*$`)
)

// eventBase is an episode title without its part marker, and the marker's number (0 when it has none).
func eventBase(title string) (string, int) {
	number := 0
	for _, marker := range []*regexp.Regexp{partNumberSuffix, partWordSuffix} {
		if found := marker.FindStringSubmatch(title); found != nil {
			number = partNumber(found[1])
			title = marker.ReplaceAllString(title, "")
			break
		}
	}
	return strings.TrimSpace(title), number
}

var partWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6}

func partNumber(text string) int {
	text = strings.ToLower(text)
	if number, err := strconv.Atoi(text); err == nil {
		return number
	}
	return partWords[text]
}

type eventEpisode struct {
	partID, workID int64
	number         int
	title, date    string
	at             time.Time
}

// DetectEvents makes an event of the episodes of two or more series of one franchise that share a title and a part
// marker and aired within weeks of each other ("Crisis on Earth-X (1)" in Tibidnibc, "(2)" in Arrow); it answers how
// many it made. Episodes already in an event, and pairs a person ruled out, are left alone.
func (c Crossovers) DetectEvents(ctx context.Context, mediaType string, categoryID int64) (int, error) {
	queries := librarydb.New(c.store.Read())
	rows, err := queries.ListFranchiseEpisodes(ctx, librarydb.ListFranchiseEpisodesParams{MediaType: mediaType, CategoryID: categoryID})
	if err != nil {
		return 0, fmt.Errorf("library: franchise episodes: %w", err)
	}
	inEvent, err := queries.ListCrossoverMemberParts(ctx, mediaType)
	if err != nil {
		return 0, fmt.Errorf("library: event episodes: %w", err)
	}
	ruledOut, err := queries.ListDifferentContentPairs(ctx)
	if err != nil {
		return 0, fmt.Errorf("library: ruled out pairs: %w", err)
	}
	apart := map[[2]int64]bool{}
	for _, pair := range ruledOut {
		apart[[2]int64{min(pair.FromID, pair.ToID), max(pair.FromID, pair.ToID)}] = true
	}
	taken := map[int64]bool{}
	for _, id := range inEvent {
		taken[id] = true
	}
	type key struct {
		franchise int64
		base      string
	}
	groups := map[key][]eventEpisode{}
	names := map[key]string{}
	for _, row := range rows {
		base, number := eventBase(row.Title)
		at, err := time.Parse("2006-01-02", row.Date)
		if err != nil || taken[row.PartID] || len(base) < 6 || genericTitle(base) {
			continue
		}
		k := key{row.FranchiseID, strings.ToLower(base)}
		groups[k] = append(groups[k], eventEpisode{partID: row.PartID, workID: row.WorkID, number: number, title: row.Title, date: row.Date, at: at})
		if names[k] == "" {
			names[k] = base
		}
	}
	created := 0
	for k, episodes := range groups {
		if !isEvent(episodes, apart) {
			continue
		}
		slices.SortFunc(episodes, func(a, b eventEpisode) int {
			if a.number != b.number && a.number != 0 && b.number != 0 {
				return a.number - b.number
			}
			if byDate := strings.Compare(a.date, b.date); byDate != 0 {
				return byDate
			}
			return int(a.partID - b.partID)
		})
		partIDs := make([]int64, len(episodes))
		for i, episode := range episodes {
			partIDs[i] = episode.partID
		}
		if err := c.createAuto(ctx, mediaType, names[k], partIDs); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

// isEvent says the episodes are one event: of two or more series, no more than a dozen, close in time, at least one
// carrying a part marker, and none pair a person said was not one.
func isEvent(episodes []eventEpisode, apart map[[2]int64]bool) bool {
	if len(episodes) < 2 || len(episodes) > eventMaxEpisodes {
		return false
	}
	works := map[int64]bool{}
	marked := false
	first, last := episodes[0].at, episodes[0].at
	for _, episode := range episodes {
		works[episode.workID] = true
		marked = marked || episode.number != 0
		if episode.at.Before(first) {
			first = episode.at
		}
		if episode.at.After(last) {
			last = episode.at
		}
	}
	if len(works) < 2 || !marked || last.Sub(first) > eventWindow {
		return false
	}
	for i, a := range episodes {
		for _, b := range episodes[i+1:] {
			if apart[[2]int64{min(a.partID, b.partID), max(a.partID, b.partID)}] {
				return false
			}
		}
	}
	return true
}

func (c Crossovers) createAuto(ctx context.Context, mediaType, name string, partIDs []int64) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		created, err := queries.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: mediaType, Name: name, Kind: CrossoverKind, Source: AutoCrossoverSource, AddedAt: c.store.Now().Unix(),
		})
		if err != nil {
			return err
		}
		return setMembersTx(ctx, queries, created.ID, partIDs)
	})
	if err != nil {
		return fmt.Errorf("library: create event %q: %w", name, err)
	}
	return nil
}
