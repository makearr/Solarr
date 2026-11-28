package music

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
)

// videoCategories are the indexer categories a video is asked for besides the library's own audio ones: movies and audio/video.
var videoCategories = []int{2000, 3020}

// videoWanted is what a search needs to know of the video it looks for.
type videoWanted struct {
	artist, title string
	aliases       []string
	year          int
	// owned is, for an upgrade, the file the video has now: a release must strictly beat it.
	owned *score.Facts
}

// videoSpec is works.Spec for music videos and concert films: a release is the video when its name carries every word
// of the artist and of the title.
type videoSpec struct {
	reader library.SeriesReader
	works  library.Works
	search Search
}

// videoSearch is the shared candidate pipeline over video works; the zero VideoReader of Search leaves it unwired.
func (s Search) videoSearch() works.Search[videoWanted] {
	deps := works.Deps{
		Desired: s.Desired, Candidates: s.Candidates, Settings: s.Settings, Clock: s.Clock,
		Blocklist: s.Blocklist, Sightings: s.Sightings, IndexerDeadline: s.IndexerDeadline,
	}
	return works.Search[videoWanted]{Name: "music video", Spec: videoSpec{reader: s.VideoReader, works: s.Works, search: s}, Deps: deps}
}

// videoItem says the item is a video work. A video of a library that did not switch videos on is never searched: it
// is reported as an item this search does not handle.
func (s Search) videoItem(ctx context.Context, item librarydb.DesiredItem) (bool, error) {
	if s.VideoReader == (library.SeriesReader{}) {
		return false, nil
	}
	detail, err := s.Works.Detail(ctx, item.WorkID)
	if err != nil {
		return false, fmt.Errorf("work detail: %w", err)
	}
	if !detail.Work.Subtype.Valid || !slices.Contains(VideoSubtypes(), detail.Work.Subtype.String) {
		return false, nil
	}
	if s.Settings == nil || !s.Settings.Snapshot().WantsMusicVideos(item.CategoryID) {
		return true, fmt.Errorf("%w: music videos are switched off in the library", ErrTrackSearchNotSupported)
	}
	return true, nil
}

// Wanted implements works.Spec: a video wants a file until it has one, then one that beats a file below its cutoff.
func (v videoSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, _ int, now time.Time) (videoWanted, bool, error) {
	if err := works.WholeItem(item); err != nil {
		return videoWanted{}, false, err
	}
	basics, err := v.reader.Basics(ctx, item.WorkID)
	if err != nil {
		return videoWanted{}, false, err
	}
	detail, err := v.works.Detail(ctx, item.WorkID)
	if err != nil {
		return videoWanted{}, false, err
	}
	wanted := videoWanted{artist: primaryArtistName(detail), title: basics.Title, aliases: basics.Aliases, year: int(basics.Year)}
	if wanted.artist == "" {
		return wanted, false, ErrArtistUnknown
	}
	if item.PresentCount == 0 {
		return wanted, true, nil
	}
	if v.search.Settings == nil {
		return wanted, false, nil
	}
	files, err := works.ItemFiles(ctx, v.reader, item.ID)
	if err != nil {
		return wanted, false, err
	}
	wanted.owned = works.OwnedBelowCutoff(ctx, v.search.Settings.Snapshot(), item.CategoryID, files, now)
	return wanted, wanted.owned != nil, nil
}

// Owned implements works.Spec.
func (videoSpec) Owned(w videoWanted) map[int]score.Facts { return works.OwnedOf(w.owned) }

// Queries implements works.Spec: the artist and the title, with the year first when it is known, then the aliases.
func (videoSpec) Queries(w videoWanted) [][]judge.Query {
	artist := releasefacts.CleanQuery(w.artist)
	names := append([]string{w.title}, w.aliases...)
	if len(names) > 3 {
		names = names[:3]
	}
	var withYear, bare []judge.Query
	for _, name := range names {
		clean := releasefacts.CleanQuery(name)
		if clean == "" {
			continue
		}
		text := strings.TrimSpace(artist + " " + clean)
		bare = append(bare, judge.Query{Function: "search", Text: text, Limit: candidateSearchResultLimit, MoreCategories: videoCategories})
		if w.year > 0 {
			withYear = append(withYear, judge.Query{Function: "search", Text: fmt.Sprintf("%s %d", text, w.year), Limit: candidateSearchResultLimit, MoreCategories: videoCategories})
		}
	}
	var tiers [][]judge.Query
	if len(withYear) > 0 {
		tiers = append(tiers, withYear)
	}
	return append(tiers, bare)
}

// Read implements works.Spec: a release is the video when its name carries every word of the artist and of one of the
// titles; a year in the name must be the video's give or take one.
func (videoSpec) Read(w videoWanted, raw indexer.RawItem, conn judge.Conn, _ time.Time) (works.Release, bool) {
	info, ok := parsemovie.Parse(raw.Title)
	if !ok {
		return works.Release{}, false
	}
	named := wordsOf(info.Title)
	if !containsAll(named, wordsOf(w.artist)) {
		return works.Release{}, false
	}
	matched := false
	for _, title := range append([]string{w.title}, w.aliases...) {
		if words := wordsOf(title); len(words) > 0 && containsAll(named, words) {
			matched = true
		}
	}
	if !matched || (w.year > 0 && info.Year > 0 && (info.Year-w.year > 1 || w.year-info.Year > 1)) {
		return works.Release{}, false
	}
	facts := releasefacts.FromRelease(info.Claims, info.ReleaseGroup, raw, conn.Impl)
	return works.Release{Facts: facts, ReleaseGroup: info.ReleaseGroup, Revised: info.Claims.Version > 1}, true
}

// wordsOf is the lower-case letters and digits of a name, split into words.
func wordsOf(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func containsAll(words, wanted []string) bool {
	for _, word := range wanted {
		if !slices.Contains(words, word) {
			return false
		}
	}
	return true
}
