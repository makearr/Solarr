package tv

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsetv"
)

// ProviderContinued names a Review choice for a file numbered past its season's end: its external id is the series'
// TheTVDB id, the season and the episode it really is ("78804:2:1").
const ProviderContinued = "tv:continued"

// shortestTitleKey keeps a name that normalises to almost nothing from matching an episode.
const shortestTitleKey = 4

// episodeLister reads a series' episodes for a second look at files its numbers could not place.
type episodeLister interface {
	Get(ctx context.Context, categoryID, workID int64, now time.Time) (library.SeriesDetailRow, error)
}

// secondLook reads one series' episodes once, for the files of a folder that its numbers did not place.
type secondLook struct {
	reader     episodeLister
	categoryID int64
	workID     int64
	now        time.Time
	read       bool
	detail     library.SeriesDetailRow
}

func (l *secondLook) series(ctx context.Context) library.SeriesDetailRow {
	if !l.read {
		l.read = true
		if l.workID != 0 && l.reader != nil {
			// A failed read only leaves the files in the queue.
			l.detail, _ = l.reader.Get(ctx, l.categoryID, l.workID, l.now)
		}
	}
	return l.detail
}

// byTitle is the episode an unnumbered file's name is the exact, unique title of (ADR-41: a folder of documentaries
// named after their episodes), nothing otherwise.
func (l *secondLook) byTitle(ctx context.Context, relPath string) []library.PartKey {
	episodes := l.series(ctx).Episodes
	stem := strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	names := []string{stem}
	if at := strings.LastIndex(stem, " - "); at >= 0 {
		names = append(names, stem[at+3:])
	}
	for _, name := range names {
		key := episodeKey(name)
		if len(key) < shortestTitleKey {
			continue
		}
		var found []library.PartKey
		for _, episode := range episodes {
			if episodeTitled(episode, key) {
				found = append(found, library.PartKey{MediaRef: MediaRef(episode.Season, episode.Number)})
			}
		}
		if len(found) == 1 {
			return found
		}
	}
	return nil
}

// episodeTitled is whether an episode's title, or one it has in another language, normalises to the key.
func episodeTitled(episode library.SeriesEpisode, key string) bool {
	if episodeKey(episode.Title) == key {
		return true
	}
	for _, alt := range episode.AltTitles {
		if episodeKey(alt) == key {
			return true
		}
	}
	return false
}

// runsPast is the episode a number past the end of its season counts on to: S01E19 of a first season of 18 is S02E01.
// False when the number is inside its season or the seasons end first.
func runsPast(episodes []library.SeriesEpisode, season, number int) (int, int, bool) {
	counts := map[int]int{}
	last := 0
	for _, episode := range episodes {
		if episode.Season > 0 {
			counts[episode.Season]++
			last = max(last, episode.Season)
		}
	}
	if counts[season] == 0 || number <= counts[season] {
		return 0, 0, false
	}
	for ; season <= last; season++ {
		if number <= counts[season] {
			return season, number, true
		}
		number -= counts[season]
	}
	return 0, 0, false
}

// continued is the Review choice for a numbered file its series has no such episode for, when counting on past the
// season's end lands on an episode. A release that numbers a whole show as one season duplicates the seasons held.
func (l *secondLook) continued(ctx context.Context, relPath string) (model.Identification, bool) {
	info, ok := parsetv.ParsePath(relPath, l.now)
	if !ok || info.Special || info.SeasonNumber == 0 || len(info.EpisodeNumbers) == 0 {
		return model.Identification{}, false
	}
	series := l.series(ctx)
	tvdb, known := series.IDs[ProviderTVDB]
	if !known {
		return model.Identification{}, false
	}
	group, parts, found := titledEpisode(series.Episodes, relPath)
	if !found {
		if group, parts, found = countedOn(series.Episodes, info); !found {
			return model.Identification{}, false
		}
	}
	return model.Identification{
		Strategy:   "numbering",
		ExternalID: &model.ExternalID{Provider: ProviderContinued, ExternalID: continuedID(tvdb, group, parts), Confidence: 1},
		Precedence: model.PrecedenceSearched,
		Confidence: 0.5,
		Evidence:   []model.MatchEvidence{{Kind: "parse", Detail: continuedDetail(series, info.SeasonNumber, group, parts)}},
	}, true
}

// titledEpisode is the one episode the title a file's name carries after its number names, the stronger evidence.
func titledEpisode(episodes []library.SeriesEpisode, relPath string) (int, []int, bool) {
	title := fileEpisodeTitle(relPath)
	if len(title) < shortestTitleKey {
		return 0, nil, false
	}
	var found []library.SeriesEpisode
	for _, episode := range episodes {
		if episode.Season > 0 && episodeTitled(episode, title) {
			found = append(found, episode)
		}
	}
	if len(found) != 1 {
		return 0, nil, false
	}
	return found[0].Season, []int{found[0].Number}, true
}

// countedOn is where a file's numbers count on to, all in one season.
func countedOn(episodes []library.SeriesEpisode, info parsetv.Info) (int, []int, bool) {
	group := 0
	var parts []int
	for _, number := range info.EpisodeNumbers {
		season, episode, past := runsPast(episodes, info.SeasonNumber, number)
		if !past || (group != 0 && season != group) {
			return 0, nil, false
		}
		group = season
		parts = append(parts, episode)
	}
	return group, parts, true
}

func continuedDetail(series library.SeriesDetailRow, pastSeason, group int, parts []int) string {
	first := parts[0]
	label := fmt.Sprintf("S%02dE%02d", group, first)
	title, held := "", false
	for _, episode := range series.Episodes {
		if episode.Season == group && episode.Number == first {
			title, held = episode.Title, episode.Present
		}
	}
	if held {
		return fmt.Sprintf("Duplicate episode: a second copy of %s %q, numbered on past season %d", label, title, pastSeason)
	}
	return fmt.Sprintf("%s %q, numbered on past season %d", label, title, pastSeason)
}

func continuedID(tvdb string, group int, parts []int) string {
	numbers := make([]string, len(parts))
	for index, part := range parts {
		numbers[index] = strconv.Itoa(part)
	}
	return tvdb + ":" + strconv.Itoa(group) + ":" + strings.Join(numbers, ",")
}

// ReadContinued reads a ProviderContinued external id.
func ReadContinued(externalID string) (tvdb string, group int, parts []int, ok bool) {
	fields := strings.Split(externalID, ":")
	if len(fields) != 3 {
		return "", 0, nil, false
	}
	group, err := strconv.Atoi(fields[1])
	if err != nil || group <= 0 {
		return "", 0, nil, false
	}
	for _, field := range strings.Split(fields[2], ",") {
		part, err := strconv.Atoi(field)
		if err != nil || part <= 0 {
			return "", 0, nil, false
		}
		parts = append(parts, part)
	}
	return fields[0], group, parts, true
}
