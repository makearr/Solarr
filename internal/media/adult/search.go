package adult

import (
	"context"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parseadult"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports an item the scene search does not handle: not the one wanted item of a scene.
var ErrNotSearchable = works.ErrNotSearchable

// FeedCategories are Newznab's XXX categories, all of them: indexers file a scene under the one of its container.
var FeedCategories = []int{6000, 6010, 6020, 6030, 6040, 6050, 6060, 6070, 6080, 6090}

// Search is the scene specialisation of the shared candidate pipeline (works.Search): what a scene wants, the queries
// scene groups' names answer to, and whether a release is the scene.
type Search struct {
	Desired    library.Desired
	Reader     library.SeriesReader
	Physical   library.Physical
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the scene's own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

func (s Search) shared() works.Search[wanted] {
	return works.Search[wanted]{
		Name: MediaType, Spec: sceneSpec{s}, Desired: s.Desired, Candidates: s.Candidates, Settings: s.Settings, Clock: s.Clock,
		Blocklist: s.Blocklist, Sightings: s.Sightings, ReleaseFilters: s.ReleaseFilters, IndexerDeadline: s.IndexerDeadline,
	}
}

// SearchItem searches one scene's item.
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// SearchPart implements works.Searcher; a scene has no parts, so it is SearchItem.
func (s Search) SearchPart(ctx context.Context, itemID int64, _ int, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// JudgeFeed judges releases a feed listed against one wanted scene, exactly as a search's would be.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error) {
	return s.shared().JudgeFeed(ctx, itemID, conn, raws, at)
}

// wanted is what a search needs to know of a scene it looks for.
type wanted struct {
	title      string
	studio     string
	date       string
	performers []string
	runtimeMs  int64
	// owned is, for an upgrade, the facts of the file the scene has now: a release must strictly beat it.
	owned *score.Facts
}

// sceneSpec is works.Spec for scenes.
type sceneSpec struct{ search Search }

// Wanted implements works.Spec: a scene wants a file until it has one, then one that beats a file below its cutoff.
func (b sceneSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, _ int, now time.Time) (wanted, bool, error) {
	if err := works.WholeItem(item); err != nil {
		return wanted{}, false, err
	}
	s := b.search
	basics, err := s.Reader.Basics(ctx, item.WorkID)
	if err != nil {
		return wanted{}, false, err
	}
	facts, err := NamingFacts(ctx, s.Reader, item.CategoryID, item.WorkID, now)
	if err != nil {
		return wanted{}, false, err
	}
	w := wanted{title: basics.Title, studio: facts.Studio, date: facts.Date, performers: facts.Performers, runtimeMs: basics.RuntimeMs}
	if item.PresentCount == 0 {
		return w, true, nil
	}
	if s.Settings == nil {
		return w, false, nil
	}
	files, err := itemFiles(ctx, s.Reader, item.ID)
	if err != nil {
		return w, false, err
	}
	if owned, below := works.BelowCutoff(s.Settings.Snapshot(), item.CategoryID, files, works.UpgradeNewest(ctx, now))[""]; below {
		w.owned = &owned.Facts
	}
	return w, w.owned != nil, nil
}

// Owned implements works.Spec.
func (sceneSpec) Owned(w wanted) map[int]score.Facts {
	if w.owned == nil {
		return nil
	}
	return map[int]score.Facts{0: *w.owned}
}

// Queries implements works.Spec: the studio and the day a scene group names a release by, then the studio and title, then
// the title with its first performer.
func (sceneSpec) Queries(w wanted) [][]judge.Query {
	title := releasefacts.CleanQuery(w.title)
	if title == "" {
		return nil
	}
	studio := releasefacts.CleanQuery(w.studio)
	var tiers [][]judge.Query
	if studio != "" && len(w.date) == 10 {
		compact := strings.ReplaceAll(studio, " ", "")
		day := w.date[2:4] + " " + w.date[5:7] + " " + w.date[8:10]
		tiers = append(tiers, []judge.Query{{Function: "search", Text: compact + " " + day}, {Function: "search", Text: studio + " " + day}})
	}
	var named []judge.Query
	if studio != "" {
		named = append(named, judge.Query{Function: "search", Text: studio + " " + title})
	}
	if len(w.performers) > 0 {
		if performer := releasefacts.CleanQuery(w.performers[0]); performer != "" {
			named = append(named, judge.Query{Function: "search", Text: performer + " " + title})
		}
	}
	if len(named) > 0 {
		tiers = append(tiers, named)
	}
	return append(tiers, []judge.Query{{Function: "search", Text: title}})
}

// Read implements works.Spec: a release is the scene when it is dated the scene's day by its studio, or names its title.
func (sceneSpec) Read(w wanted, raw indexer.RawItem, conn judge.Conn, _ time.Time) (works.Release, bool) {
	if !w.isTheScene(raw.Title) {
		return works.Release{}, false
	}
	info, _ := parseadult.ParseRelease(raw.Title)
	claims := parsetv.ParseClaims(raw.Title)
	facts := releasefacts.FromRelease(claims, info.Group, raw, conn.Impl)
	releasefacts.EstimateBitrate(&facts, raw.SizeBytes, 1, w.runtimeMs)
	return works.Release{Facts: facts, ReleaseGroup: info.Group, Revised: claims.Version > 1}, true
}

// isTheScene says a release name is this scene's. A dated release is the scene on its day when its studio matches (the
// group writes "NightStudio" for "Night Studio") or its words hold the title or a performer; an undated one needs the
// title and the studio.
func (w wanted) isTheScene(name string) bool {
	words := " " + NameKey(name) + " "
	titleKey := NameKey(w.title)
	hasTitle := titleKey != "" && strings.Contains(words, " "+titleKey+" ")
	studioKey := compact(w.studio)
	if info, dated := parseadult.ParseRelease(name); dated {
		if w.date == "" || info.Date != w.date {
			return false
		}
		if studioKey != "" && compact(info.Studio) == studioKey || hasTitle {
			return true
		}
		for _, performer := range w.performers {
			if key := NameKey(performer); key != "" && strings.Contains(words, " "+key+" ") {
				return true
			}
		}
		return false
	}
	return hasTitle && (studioKey == "" || strings.Contains(compact(name), studioKey))
}

// compact is a name with no spaces: how scene groups write a studio.
func compact(name string) string { return strings.ReplaceAll(NameKey(name), " ", "") }

// itemFiles are a scene's files as the cutoff check and the upgrade search judge them.
func itemFiles(ctx context.Context, reader library.SeriesReader, itemID int64) ([]works.File, error) {
	files, err := reader.ItemFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]works.File, len(files))
	for index, file := range files {
		out[index] = works.File{
			MediaObjectID: file.File.MediaObjectID, AddedAt: file.AddedAt, Measured: file.Measured, Monitored: file.Monitored,
			Facts: releasefacts.FromFile(file.File),
		}
	}
	return out, nil
}
