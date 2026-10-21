package judge_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

const webProfile = `{"quality_profile":"WEB 1080p","require":{"resolution":{"min":"720p","max":"1080p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["1080p"],["720p"]],"source":[["webdl"],["webrip"],["hdtv"]]}}`
const hdProfile = `{"quality_profile":"HD","require":{"resolution":{"min":"720p","max":"1080p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["1080p"],["720p"]],"source":[["hdtv"],["webdl"]]}}`

func profiles(t *testing.T, documents ...string) []judge.RankedProfile {
	t.Helper()
	var ranked []judge.RankedProfile
	for _, document := range documents {
		rules, err := score.Parse([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		ranked = append(ranked, judge.RankedProfile{Name: rules.Name, Rules: rules})
	}
	return ranked
}

func facts(resolution, source string) score.Facts {
	return score.Facts{File: score.FileFacts{Resolution: resolution, Source: source}}
}

func TestEvaluateInOrderPrefersWhatTheFirstProfileWants(t *testing.T) {
	ranked := profiles(t, webProfile, hdProfile)
	web := judge.EvaluateInOrder(ranked, facts("1080p", "webdl"), nil)
	hd := judge.EvaluateInOrder(ranked, facts("1080p", "hdtv"), nil)
	if web.Outcome != model.OutcomeAccept || hd.Outcome != model.OutcomeAccept {
		t.Fatalf("outcomes = %v, %v", web.Outcome, hd.Outcome)
	}
	if !judge.StrictlyBetter(web, hd) || judge.StrictlyBetter(hd, web) {
		t.Error("the release the first profile fits must beat one only a later profile accepts")
	}
	if tooSmall := judge.EvaluateInOrder(ranked, facts("480p", "hdtv"), nil); tooSmall.Outcome == model.OutcomeAccept {
		t.Error("480p is below every profile's minimum")
	}
}

func TestRevisionBreaksTiesOnlyAfterQuality(t *testing.T) {
	ranked := profiles(t, webProfile)
	original := judge.WithRevision(judge.EvaluateInOrder(ranked, facts("1080p", "webdl"), nil), false, true)
	proper := judge.WithRevision(judge.EvaluateInOrder(ranked, facts("1080p", "webdl"), nil), true, true)
	worseButProper := judge.WithRevision(judge.EvaluateInOrder(ranked, facts("720p", "webdl"), nil), true, true)
	if !judge.StrictlyBetter(proper, original) {
		t.Error("a proper beats the same quality")
	}
	if judge.StrictlyBetter(worseButProper, original) {
		t.Error("a proper never beats better quality")
	}
}

func TestFindsRankAcceptedFirstThenQualityThenDistance(t *testing.T) {
	ranked := profiles(t, webProfile)
	good := judge.Find{Raw: indexer.RawItem{Title: "good"}, Decision: judge.EvaluateInOrder(ranked, facts("1080p", "webdl"), nil)}
	lower := judge.Find{Raw: indexer.RawItem{Title: "lower"}, Decision: judge.EvaluateInOrder(ranked, facts("720p", "webdl"), nil)}
	rejected := judge.Find{Raw: indexer.RawItem{Title: "rejected"}, Decision: judge.EvaluateInOrder(ranked, facts("480p", "dvd"), nil)}
	finds := []judge.Find{rejected, lower, good}
	judge.SortBestFirst(finds)
	if finds[0].Raw.Title != "good" || finds[1].Raw.Title != "lower" || finds[2].Raw.Title != "rejected" {
		t.Errorf("order = %s, %s, %s", finds[0].Raw.Title, finds[1].Raw.Title, finds[2].Raw.Title)
	}
}

func TestDuplicatesAcrossIndexersKeepTheFirst(t *testing.T) {
	a := judge.Find{Conn: judge.Conn{ID: 1}, Raw: indexer.RawItem{Title: "Show.S01E01.1080p", SizeBytes: 1000}}
	b := judge.Find{Conn: judge.Conn{ID: 2}, Raw: indexer.RawItem{Title: "show s01e01 1080p", SizeBytes: 1005}}
	c := judge.Find{Conn: judge.Conn{ID: 2}, Raw: indexer.RawItem{Title: "Show.S01E01.1080p", SizeBytes: 5000}}
	kept := judge.WithoutCrossIndexerDuplicates([]judge.Find{a, b, c})
	if len(kept) != 2 || kept[0].Conn.ID != 1 || kept[1].Raw.SizeBytes != 5000 {
		t.Errorf("kept = %+v", kept)
	}
}

func TestProtocolDelayDefersTheOtherProtocolInsideTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	accepted := model.Decision{Outcome: model.OutcomeAccept}
	finds := []judge.Find{
		{Conn: judge.Conn{Impl: "newznab"}, Raw: indexer.RawItem{PublishedAt: now.Add(-time.Hour)}, Decision: accepted},
		{Conn: judge.Conn{Impl: "torznab"}, Raw: indexer.RawItem{PublishedAt: now.Add(-time.Hour)}, Decision: accepted},
		{Conn: judge.Conn{Impl: "torznab"}, Raw: indexer.RawItem{PublishedAt: now.Add(-5 * time.Hour)}, Decision: accepted},
	}
	judge.ProtocolDelay{Preferred: "usenet", Window: 3 * time.Hour, Now: now}.Apply(finds)
	if finds[0].Decision.Outcome != model.OutcomeAccept || finds[1].Decision.Outcome != model.OutcomeDefer || finds[2].Decision.Outcome != model.OutcomeAccept {
		t.Errorf("outcomes = %v %v %v", finds[0].Decision.Outcome, finds[1].Decision.Outcome, finds[2].Decision.Outcome)
	}
}

type fakeSearcher struct {
	items []indexer.RawItem
	err   error
	seen  []indexer.SearchRequest
}

func (f *fakeSearcher) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	f.seen = append(f.seen, request)
	return f.items, f.err
}

func TestAskSkipsQueriesAnIndexerCannotTakeAndDeduplicatesByGUID(t *testing.T) {
	capable := &fakeSearcher{items: []indexer.RawItem{{GUID: "a"}, {GUID: "b"}}}
	limited := &fakeSearcher{items: []indexer.RawItem{{GUID: "c"}}}
	conns := []judge.Conn{
		{ID: 1, Client: capable, Params: map[string][]string{"tvsearch": {"q", "tvdbid", "season", "ep"}}},
		{ID: 2, Client: limited, Params: map[string][]string{"tvsearch": {"q"}}},
	}
	queries := []judge.Query{
		{Function: "tvsearch", Extra: map[string]string{"tvdbid": "1"}, Needs: []string{"tvdbid"}},
		{Function: "tvsearch", Text: "show s01"},
	}
	answers, err := judge.AskEveryIndexer(t.Context(), conns, queries, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(capable.seen) != 2 || len(limited.seen) != 1 || limited.seen[0].Query != "show s01" {
		t.Errorf("requests: capable %d, limited %+v", len(capable.seen), limited.seen)
	}
	if len(answers[0].Raws) != 2 {
		t.Errorf("a release listed by both queries is one: %+v", answers[0].Raws)
	}
}

// An indexer without TVDB ids is asked by text even though another found the season by id (found live 2026-10-21:
// The Pirate Bay was never asked for TV while NZBgeek answered); one that found stops widening.
func TestAskThroughTiersWidensEachIndexerOnItsOwn(t *testing.T) {
	byID := &fakeSearcher{items: []indexer.RawItem{{GUID: "id-1"}}}
	textOnly := &fakeSearcher{items: []indexer.RawItem{{GUID: "text-1"}}}
	empty := &fakeSearcher{}
	conns := []judge.Conn{
		{ID: 1, Client: byID, Params: map[string][]string{"tvsearch": {"q", "tvdbid", "season"}}},
		{ID: 2, Client: textOnly, Params: map[string][]string{"tvsearch": {"q", "season"}}},
		{ID: 3, Client: empty, Params: map[string][]string{"tvsearch": {"q", "tvdbid", "season"}}},
	}
	tiers := [][]judge.Query{
		{{Function: "tvsearch", Extra: map[string]string{"tvdbid": "1", "season": "1"}, Needs: []string{"tvdbid", "season"}}},
		{{Function: "search", Text: "show s01"}},
	}
	walk, err := judge.AskThroughTiers(t.Context(), conns, tiers, time.Second, func(conn judge.Conn, raws []indexer.RawItem) ([]judge.Find, error) {
		finds := make([]judge.Find, len(raws))
		for index, raw := range raws {
			finds[index] = judge.Find{Conn: conn, Raw: raw}
		}
		return finds, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var guids []string
	for _, find := range walk.Finds {
		guids = append(guids, find.Raw.GUID)
	}
	if !slices.Equal(guids, []string{"id-1", "text-1"}) || walk.TierUsed == nil || *walk.TierUsed != 0 {
		t.Errorf("finds = %v, tier used = %v", guids, walk.TierUsed)
	}
	asked := []int{len(byID.seen), len(textOnly.seen), len(empty.seen)}
	if !slices.Equal(asked, []int{1, 1, 2}) || textOnly.seen[0].Query != "show s01" {
		t.Errorf("requests per indexer = %v (want 1, 1, 2), text-only asked %+v", asked, textOnly.seen)
	}
}

// limitRecorder answers nothing and remembers the limit each query asked with.
type limitRecorder struct{ limits []int }

func (r *limitRecorder) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	r.limits = append(r.limits, request.Limit)
	return nil, nil
}

// A query asks for as many results as it names, and a query that names none for a hundred.
func TestAskSendsEachQuerysOwnLimit(t *testing.T) {
	recorder := &limitRecorder{}
	queries := []judge.Query{{Function: "music", Text: "a", Limit: 20}, {Function: "tvsearch", Text: "b"}}
	if _, err := judge.AskEveryIndexer(t.Context(), []judge.Conn{{ID: 1, Client: recorder}}, queries, time.Minute); err != nil {
		t.Fatalf("AskEveryIndexer: %v", err)
	}
	if want := []int{20, 100}; len(recorder.limits) != 2 || recorder.limits[0] != want[0] || recorder.limits[1] != want[1] {
		t.Errorf("limits asked = %v, want %v", recorder.limits, want)
	}
}

// Two indexers' listings of one release are one row however each punctuates the name; different words stay apart.
func TestDuplicatesAcrossIndexersIgnorePunctuationNotWords(t *testing.T) {
	find := func(indexerID int64, title string) judge.Find {
		return judge.Find{Conn: judge.Conn{ID: indexerID}, Raw: indexer.RawItem{Title: title, SizeBytes: 1000}}
	}
	kept := judge.WithoutCrossIndexerDuplicates([]judge.Find{
		find(1, "An Artist - Salt & Stone (2020) FLAC"),
		find(2, "An.Artist-Salt.and.Stone.2020.FLAC"),
		find(2, "An Artist - Saltand Stone (2020) FLAC"),
		find(2, "An Artist - Don't Stop (2020) FLAC"),
		find(1, "An Artist - Dont Stop (2020) FLAC"),
	})
	var titles []string
	for _, find := range kept {
		titles = append(titles, find.Raw.Title)
	}
	want := []string{
		"An Artist - Salt & Stone (2020) FLAC", "An Artist - Saltand Stone (2020) FLAC", "An Artist - Don't Stop (2020) FLAC",
	}
	if len(titles) != len(want) || titles[0] != want[0] || titles[1] != want[1] || titles[2] != want[2] {
		t.Errorf("kept = %q\nwant %q", titles, want)
	}
}
