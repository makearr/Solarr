package library

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
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
	ref            string
	number         int
	title, date    string
	at             time.Time
	// missing is set for a part the library lacks, which has no part id.
	missing *MissingPart
}

// DetectEvents makes an event of a franchise's episodes that share a title within weeks ("Crisis on Earth-X (1)" in one
// series, "(2)" in another), or extends the event of that name; it answers how many. Ruled-out, edited and deleted ones stay.
func (c Crossovers) DetectEvents(ctx context.Context, mediaType string, categoryID int64) (int, error) {
	queries := librarydb.New(c.store.Read())
	rows, err := queries.ListFranchiseEpisodes(ctx, librarydb.ListFranchiseEpisodesParams{MediaType: mediaType, CategoryID: categoryID})
	if err != nil {
		return 0, fmt.Errorf("library: franchise episodes: %w", err)
	}
	owners, err := queries.ListCrossoverMemberPartOwners(ctx, mediaType)
	if err != nil {
		return 0, fmt.Errorf("library: event episodes: %w", err)
	}
	apart, err := ruledOutPairs(ctx, queries)
	if err != nil {
		return 0, fmt.Errorf("library: %w", err)
	}
	taken := map[int64]bool{}
	for _, owner := range owners {
		taken[owner.PartID] = true
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
		groups[k] = append(groups[k], eventEpisode{partID: row.PartID, workID: row.WorkID, ref: row.MediaRef, number: number, title: row.Title, date: row.Date, at: at})
		if names[k] == "" {
			names[k] = base
		}
	}
	made := 0
	for k, episodes := range groups {
		first, last := span(episodes)
		var extended bool
		err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			write := librarydb.New(tx)
			targets, err := matchingEvents(ctx, write, mediaType, k.base, first, last)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				if !isEvent(episodes, apart) {
					return nil
				}
				if dismissed, err := dismissedEvent(ctx, write, k.base, first); err != nil || dismissed {
					return err
				}
			} else if separate(episodes, apart) {
				return nil
			}
			var target int64
			var absorb []int64
			if len(targets) > 0 {
				target, absorb = targets[0], targets[1:]
			}
			_, err = storeEvent(ctx, write, mediaType, c.store.Now(), eventInput{name: names[k], episodes: episodes}, target, absorb)
			extended = err == nil
			return err
		})
		if err != nil {
			return made, fmt.Errorf("library: event %q: %w", names[k], err)
		}
		if extended {
			made++
		}
	}
	return made, nil
}

func span(episodes []eventEpisode) (first, last time.Time) {
	for _, episode := range episodes {
		first, last = earlier(first, episode.at), later(last, episode.at)
	}
	return first, last
}

// isEvent says the episodes are one event: of two or more series, no more than a dozen, close in time, and either one
// carrying a part marker or three or more aired on different days, and none pair a person said was not one.
func isEvent(episodes []eventEpisode, apart map[[2]int64]bool) bool {
	if len(episodes) < 2 || len(episodes) > eventMaxEpisodes {
		return false
	}
	works := map[int64]bool{}
	days := map[string]bool{}
	marked := false
	for _, episode := range episodes {
		works[episode.workID] = true
		days[episode.date] = true
		marked = marked || episode.number != 0
	}
	first, last := span(episodes)
	if len(works) < 2 || last.Sub(first) > eventWindow || !marked && (len(episodes) < 3 || len(days) < 2) {
		return false
	}
	return !separate(episodes, apart)
}
