package importlist_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/service/importlist"
	"github.com/makearr/Solarr/migrations"
)

// listType is a media type whose libraries take TMDB lists: a show resolves to itself, "unknown" to nothing.
type listType struct {
	added *[]media.AddRequest
	fail  error
}

func (listType) Descriptor() media.Descriptor {
	return media.Descriptor{Key: "shows", AddKinds: []media.Choice{{Key: "show"}},
		MonitorOptions: []media.Choice{{Key: "all"}, {Key: "future"}}}
}

func (listType) ImportListTypes() []media.ListType {
	return []media.ListType{media.ListTMDBList, media.ListTMDBPopular}
}

func (listType) ResolveListEntry(_ context.Context, entry media.ListEntry) (media.ListMatch, error) {
	switch {
	case entry.Form == tmdb.MediaMovie:
		return media.ListMatch{Foreign: true}, nil
	case entry.Title == "unknown":
		return media.ListMatch{}, nil
	case entry.Title == "renamed":
		return media.ListMatch{ProviderID: media.ProviderID{Provider: "tvdb", ExternalID: "77"}, Found: true}, nil
	}
	return media.ListMatch{ProviderID: entry.Key(), Found: true}, nil
}

func (l listType) LookupWorks(context.Context, media.AddLookup) ([]media.AddCandidate, error) {
	return nil, nil
}

func (l listType) AddWork(_ context.Context, request media.AddRequest) (media.AddResult, error) {
	if l.fail != nil {
		return media.AddResult{}, l.fail
	}
	*l.added = append(*l.added, request)
	id, _ := strconv.ParseInt(request.ExternalID, 10, 64)
	return media.AddResult{WorkID: 1000 + id, Created: true}, nil
}

type memoryLists map[int64]settings.ImportList

func (m memoryLists) ImportList(_ context.Context, id int64) (settings.ImportList, error) {
	list, ok := m[id]
	if !ok {
		return settings.ImportList{}, settings.ErrNotFound
	}
	return list, nil
}

func (m memoryLists) EnabledImportLists(context.Context) ([]settings.ImportList, error) {
	var lists []settings.ImportList
	for id := int64(1); id <= int64(len(m)); id++ {
		if list, ok := m[id]; ok && list.Enabled {
			lists = append(lists, list)
		}
	}
	return lists, nil
}

type heldWorks map[media.ProviderID]int64

func (h heldWorks) WorkByProviderID(_ context.Context, _, provider, externalID string) (int64, bool, error) {
	id, ok := h[media.ProviderID{Provider: provider, ExternalID: externalID}]
	return id, ok, nil
}

type libraries map[int64]settings.Category

func (l libraries) Category(id int64) (settings.Category, bool) {
	category, ok := l[id]
	return category, ok
}

func openLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

type syncFixture struct {
	service    importlist.Service
	lists      memoryLists
	held       heldWorks
	tmdb       *fakeTMDB
	records    library.ImportLists
	exclusions library.Exclusions
	clock      *clock.Fake
	added      []media.AddRequest
	module     *listType
}

var syncStart = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func show(id int64, title string) tmdb.ListItem {
	return tmdb.ListItem{ID: id, MediaType: tmdb.MediaTV, Name: title}
}

func newSyncFixture(t *testing.T, items ...tmdb.ListItem) *syncFixture {
	t.Helper()
	opened := openLibrary(t)
	f := &syncFixture{
		lists: memoryLists{1: {ID: 1, CategoryID: 7, Name: "Shows", Type: string(media.ListTMDBList), Options: `{"list":"8"}`,
			CadenceHours: 12, Monitor: true, MonitorOption: "future", QualityProfileID: ptr(int64(3)), Enabled: true}},
		held: heldWorks{}, tmdb: &fakeTMDB{pages: []tmdb.ListPage{{TotalPages: 1, Items: items}}},
		records: library.NewImportLists(opened), exclusions: library.NewExclusions(opened), clock: clock.NewFake(syncStart),
	}
	f.module = &listType{added: &f.added}
	registry, err := media.NewRegistry(f.module)
	if err != nil {
		t.Fatal(err)
	}
	f.service = importlist.Service{
		Lists: f.lists, Records: f.records, Exclusions: f.exclusions, Works: f.held,
		Libraries: libraries{7: {ID: 7, MediaType: "shows"}}, Media: registry,
		Catalogue: importlist.NewCatalogue(&fakeAniList{}, f.tmdb, fall2026), Clock: f.clock, Logger: slog.New(slog.DiscardHandler),
	}
	return f
}

func ptr[T any](value T) *T { return &value }

func (f *syncFixture) run(t *testing.T, listID int64) {
	t.Helper()
	payload, _ := json.Marshal(importlist.Payload{ListID: listID})
	if err := f.service.Run(t.Context(), jobs.Job{Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func (f *syncFixture) addedIDs() []string {
	var ids []string
	for _, request := range f.added {
		ids = append(ids, request.Provider+":"+request.ExternalID)
	}
	return ids
}

type verdictRow struct {
	Title   string
	Verdict importlist.Verdict
	Match   string
	WorkID  int64
}

func verdicts(judged []importlist.Judged) []verdictRow {
	var rows []verdictRow
	for _, entry := range judged {
		match := ""
		if entry.Match.Provider != "" {
			match = entry.Match.Provider + ":" + entry.Match.ExternalID
		}
		rows = append(rows, verdictRow{entry.Entry.Title, entry.Verdict, match, entry.WorkID})
	}
	return rows
}

func TestPreviewJudgesEveryEntryAndAddsNothing(t *testing.T) {
	f := newSyncFixture(t,
		show(1, "New Show"), show(2, "Held Show"), show(3, "Excluded Show"), show(4, "Skip Me"),
		tmdb.ListItem{ID: 5, MediaType: tmdb.MediaMovie, Title: "A Film"}, show(6, "unknown"), show(7, "renamed"),
		show(8, "Excluded By Its Match"), show(1, "New Show"),
	)
	f.held[media.ProviderID{Provider: "tmdb", ExternalID: "2"}] = 52
	f.held[media.ProviderID{Provider: "tvdb", ExternalID: "77"}] = 77
	f.lists[1] = withExclusions(f.lists[1], "skip me, tmdb:8")
	if err := f.exclusions.Exclude(t.Context(), syncStart, "shows", "Excluded Show", []library.ExternalIDWrite{{Provider: "tmdb", ExternalID: "3"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.exclusions.Exclude(t.Context(), syncStart, "films", "Same id, other type", []library.ExternalIDWrite{{Provider: "tmdb", ExternalID: "1"}}); err != nil {
		t.Fatal(err)
	}

	judged, err := f.service.Preview(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []verdictRow{
		{"New Show", importlist.VerdictAdd, "tmdb:1", 0},
		{"Held Show", importlist.VerdictHeld, "", 52},
		{"Excluded Show", importlist.VerdictExcluded, "", 0},
		{"Skip Me", importlist.VerdictFiltered, "", 0},
		{"A Film", importlist.VerdictForeign, "", 0},
		{"unknown", importlist.VerdictUnknown, "", 0},
		{"renamed", importlist.VerdictHeld, "tvdb:77", 77},
		{"Excluded By Its Match", importlist.VerdictFiltered, "", 0},
	}
	if got := verdicts(judged); !reflect.DeepEqual(got, want) {
		t.Errorf("preview =\n%+v\nwant\n%+v", got, want)
	}
	if len(f.added) != 0 {
		t.Errorf("a preview added %v", f.addedIDs())
	}
	if entries, _ := f.records.Entries(t.Context(), 1); len(entries) != 0 {
		t.Errorf("a preview kept entries %+v", entries)
	}
	previewed, err := f.service.IsPreviewed(t.Context(), f.lists[1])
	if err != nil || !previewed {
		t.Errorf("previewed = %v, err = %v", previewed, err)
	}
	changed := f.lists[1]
	changed.Options = `{"list":"9"}`
	if previewed, _ := f.service.IsPreviewed(t.Context(), changed); previewed {
		t.Error("a list whose settings changed after its preview counts as previewed")
	}
	reordered := f.lists[1]
	reordered.Options = `{ "list" : "8" }`
	if previewed, _ := f.service.IsPreviewed(t.Context(), reordered); !previewed {
		t.Error("the same settings written differently need another preview")
	}
}

func withExclusions(list settings.ImportList, exclusions string) settings.ImportList {
	list.Exclusions = exclusions
	return list
}

func TestARunAddsWithTheListsChoicesAndNeverTheSameEntryTwice(t *testing.T) {
	f := newSyncFixture(t, show(1, "New Show"), show(2, "Held Show"), show(6, "unknown"))
	f.held[media.ProviderID{Provider: "tmdb", ExternalID: "2"}] = 52
	f.run(t, 1)
	want := []media.AddRequest{{CategoryID: 7, Kind: "show", Provider: "tmdb", ExternalID: "1", Monitored: true, Option: "future", ProfileID: ptr(int64(3))}}
	if !reflect.DeepEqual(f.added, want) {
		t.Errorf("added %+v, want %+v", f.added, want)
	}
	entries, err := f.records.Entries(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	unix := syncStart.Unix()
	wantEntries := []library.ImportListEntry{
		{ListID: 1, Provider: "tmdb", ExternalID: "1", Outcome: library.ListAdded, WorkID: 1001, Title: "New Show", SeenAt: unix},
		{ListID: 1, Provider: "tmdb", ExternalID: "2", Outcome: library.ListHeld, WorkID: 52, Title: "Held Show", SeenAt: unix},
		{ListID: 1, Provider: "tmdb", ExternalID: "6", Outcome: library.ListUnresolved, Title: "unknown", SeenAt: unix},
	}
	if !reflect.DeepEqual(entries, wantEntries) {
		t.Errorf("entries =\n%+v\nwant\n%+v", entries, wantEntries)
	}
	state, _ := f.records.State(t.Context(), 1)
	if want := (library.ImportListState{ListID: 1, RunAt: unix, RunAdded: 1}); state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}

	// The person removed the added show and the held one: neither is held now, and neither comes back.
	delete(f.held, media.ProviderID{Provider: "tmdb", ExternalID: "2"})
	f.added = nil
	f.run(t, 1)
	if len(f.added) != 0 {
		t.Errorf("a second run added %v again", f.addedIDs())
	}
}

func TestTheSweepRunsAListOnlyOnceItsCadenceHasPassed(t *testing.T) {
	f := newSyncFixture(t, show(1, "First"))
	f.run(t, 0)
	if !reflect.DeepEqual(f.addedIDs(), []string{"tmdb:1"}) {
		t.Fatalf("first sweep added %v", f.addedIDs())
	}
	f.tmdb.pages[0].Items = append(f.tmdb.pages[0].Items, show(2, "Second"))
	f.clock.Advance(11 * time.Hour)
	f.run(t, 0)
	if len(f.added) != 1 {
		t.Errorf("a sweep within the 12-hour cadence added %v", f.addedIDs())
	}
	f.clock.Advance(time.Hour)
	f.run(t, 0)
	if !reflect.DeepEqual(f.addedIDs(), []string{"tmdb:1", "tmdb:2"}) {
		t.Errorf("after the cadence added %v", f.addedIDs())
	}
}

func TestADisabledListIsNeverRun(t *testing.T) {
	f := newSyncFixture(t, show(1, "First"))
	disabled := f.lists[1]
	disabled.Enabled = false
	f.lists[1] = disabled
	f.run(t, 0)
	f.run(t, 1)
	if len(f.added) != 0 {
		t.Errorf("a disabled list added %v", f.addedIDs())
	}
}

func TestAnEntryNoSourceKnewIsLookedUpAgainAfterAWeek(t *testing.T) {
	f := newSyncFixture(t, show(6, "unknown"))
	f.run(t, 1)
	judged, _ := f.service.Preview(t.Context(), 1)
	if got := verdicts(judged); !reflect.DeepEqual(got, []verdictRow{{"unknown", importlist.VerdictHandled, "", 0}}) {
		t.Errorf("the next day: %+v", got)
	}
	f.clock.Advance(8 * 24 * time.Hour)
	judged, _ = f.service.Preview(t.Context(), 1)
	if got := verdicts(judged); !reflect.DeepEqual(got, []verdictRow{{"unknown", importlist.VerdictUnknown, "", 0}}) {
		t.Errorf("a week on: %+v", got)
	}
}

func TestARunAddsAtMostFiftyAndTheNextRunTheRest(t *testing.T) {
	var items []tmdb.ListItem
	for id := int64(1); id <= 60; id++ {
		items = append(items, show(id, "Show "+strconv.FormatInt(id, 10)))
	}
	f := newSyncFixture(t, items...)
	f.run(t, 1)
	if len(f.added) != 50 {
		t.Fatalf("first run added %d, want 50", len(f.added))
	}
	f.run(t, 1)
	if len(f.added) != 60 || f.added[59].ExternalID != "60" {
		t.Errorf("after two runs added %d", len(f.added))
	}
}

func TestPreviewLooksUpABoundedNumberOfEntries(t *testing.T) {
	var items []tmdb.ListItem
	for id := int64(1); id <= 35; id++ {
		items = append(items, show(id, "Show"))
	}
	f := newSyncFixture(t, items...)
	judged, err := f.service.Preview(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[importlist.Verdict]int{}
	for _, entry := range judged {
		counts[entry.Verdict]++
	}
	if want := map[importlist.Verdict]int{importlist.VerdictAdd: 30, importlist.VerdictUnchecked: 5}; !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %v, want %v", counts, want)
	}
}

func TestAFailingRunIsRecordedWithWhy(t *testing.T) {
	f := newSyncFixture(t, show(1, "First"))
	f.tmdb.err = tmdb.ErrNoKey
	f.run(t, 1)
	state, _ := f.records.State(t.Context(), 1)
	if want := (library.ImportListState{ListID: 1, RunAt: syncStart.Unix(), RunFailure: "needs_key"}); state != want {
		t.Errorf("state = %+v, want %+v", state, want)
	}

	f.tmdb.err = nil
	f.module.fail = media.ErrSourceUnavailable
	f.run(t, 1)
	state, _ = f.records.State(t.Context(), 1)
	if state.RunFailure != "source_unavailable" {
		t.Errorf("add outage: state = %+v", state)
	}
	if entries, _ := f.records.Entries(t.Context(), 1); len(entries) != 0 {
		t.Errorf("an entry whose add failed was kept: %+v", entries)
	}
}

func TestCheckRefusesWhatTheLibraryCannotTake(t *testing.T) {
	f := newSyncFixture(t)
	request := settings.ImportListRequest{Name: "x", Type: string(media.ListTMDBList), Options: `{"list":"8"}`, CadenceHours: 24, MonitorOption: "future"}
	if err := f.service.Check(7, request); err != nil {
		t.Errorf("a fine list: %v", err)
	}
	request.Type, request.MonitorOption = string(media.ListAniListUser), "everything"
	var problems config.Problems
	if err := f.service.Check(7, request); !errors.As(err, &problems) {
		t.Fatalf("err = %v", err)
	}
	want := config.Problems{
		{Path: "type", Value: "anilist_user", Expectation: "a list type this library takes"},
		{Path: "monitor_option", Value: "everything", Expectation: "one of the library's monitor options"},
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems = %+v, want %+v", problems, want)
	}
	if err := f.service.Check(99, request); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("no library: err = %v", err)
	}
}
