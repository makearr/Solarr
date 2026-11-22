package serverpush_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/serverpush"
)

type fakeSettings struct {
	servers   []settings.MediaServer
	libraries map[int64][]settings.MediaServerLibrary
	paths     map[int64][]settings.MediaServerPath
	results   map[int64]string
}

func (f *fakeSettings) MediaServers(context.Context) ([]settings.MediaServer, error) {
	return f.servers, nil
}
func (f *fakeSettings) AllMediaServerLibraries(context.Context) (map[int64][]settings.MediaServerLibrary, error) {
	return f.libraries, nil
}
func (f *fakeSettings) MediaServerPaths(_ context.Context, id int64) ([]settings.MediaServerPath, error) {
	return f.paths[id], nil
}
func (f *fakeSettings) MediaServerPushDebounceSeconds() int64 { return 0 }

func (f *fakeSettings) SetMediaServerResult(_ context.Context, id, _ int64, message string) error {
	f.results[id] = message
	return nil
}

type fakeRoots struct{}

func (fakeRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	return settings.RootFolder{ID: id, Path: "/media/shows"}, id == 1
}

type fakeQueue struct {
	rows  []library.PendingPush
	files []library.WorkFilePlace
	next  int64
}

func (q *fakeQueue) Add(_ context.Context, rows []library.PendingPush) error {
	for _, row := range rows {
		q.next++
		row.ID = q.next
		q.rows = append(q.rows, row)
	}
	return nil
}
func (q *fakeQueue) Due(_ context.Context, serverID, cutoff, limit int64) ([]library.PendingPush, error) {
	var due []library.PendingPush
	for _, row := range q.rows {
		if row.ServerID == serverID && row.QueuedAt <= cutoff && int64(len(due)) < limit {
			due = append(due, row)
		}
	}
	return due, nil
}
func (q *fakeQueue) ServersDue(_ context.Context, cutoff int64) ([]int64, error) {
	var ids []int64
	for _, row := range q.rows {
		if row.QueuedAt <= cutoff {
			ids = append(ids, row.ServerID)
		}
	}
	return ids, nil
}
func (q *fakeQueue) Delete(_ context.Context, ids []int64) error {
	kept := q.rows[:0:0]
	for _, row := range q.rows {
		gone := false
		for _, id := range ids {
			gone = gone || id == row.ID
		}
		if !gone {
			kept = append(kept, row)
		}
	}
	q.rows = kept
	return nil
}
func (q *fakeQueue) Bump(_ context.Context, ids []int64) error {
	for i := range q.rows {
		for _, id := range ids {
			if q.rows[i].ID == id {
				q.rows[i].Attempts++
			}
		}
	}
	return nil
}
func (q *fakeQueue) WorkTitles(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{5: "Show A"}, nil
}
func (q *fakeQueue) WorkFiles(context.Context, int64) ([]library.WorkFilePlace, error) {
	return q.files, nil
}

type fakePusher struct {
	err  error
	sent [][]mediaserver.Change
}

func (p *fakePusher) Changed(_ context.Context, changes []mediaserver.Change) error {
	p.sent = append(p.sent, changes)
	return p.err
}

type announcingPusher struct {
	fakePusher
	said []string
}

func (p *announcingPusher) Announce(_ context.Context, text string) error {
	p.said = append(p.said, text)
	return nil
}

type fakeClients struct{ pusher mediaserver.Pusher }

func (c fakeClients) Pusher(context.Context, settings.MediaServer) (mediaserver.Pusher, error) {
	return c.pusher, nil
}

type fakeScheduler struct{ scheduled []int64 }

func (s *fakeScheduler) ScheduleFlush(_ context.Context, id int64, _ time.Duration) error {
	s.scheduled = append(s.scheduled, id)
	return nil
}

func fixture() (serverpush.Service, *fakeSettings, *fakeQueue, *fakePusher, *fakeScheduler, *clock.Fake) {
	fake := clock.NewFakeAtEpoch()
	config := &fakeSettings{
		servers: []settings.MediaServer{
			{ID: 1, Kind: "jellyfin", Enabled: true, MediaServerUse: settings.MediaServerUse{PushFiles: true}},
			{ID: 2, Kind: "jellyfin", Enabled: true},
			{ID: 3, Kind: "jellyfin", Enabled: true, MediaServerUse: settings.MediaServerUse{PushFiles: true}},
			{ID: 4, Kind: "calibre", Enabled: true, MediaServerUse: settings.MediaServerUse{PushFiles: true}},
		},
		libraries: map[int64][]settings.MediaServerLibrary{
			1: {{CategoryID: 7, ServerLibraryID: "lib-a"}}, 2: {{CategoryID: 7, ServerLibraryID: "lib-b"}},
			3: {{CategoryID: 8, ServerLibraryID: "lib-c"}}, 4: {{CategoryID: 7, ServerLibraryID: "lib-d"}},
		},
		paths:   map[int64][]settings.MediaServerPath{1: {{SolarrPrefix: "/media", ServerPrefix: "/data"}}},
		results: map[int64]string{},
	}
	queue := &fakeQueue{}
	pusher := &fakePusher{}
	scheduler := &fakeScheduler{}
	service := serverpush.Service{
		Settings: config, Roots: fakeRoots{}, Queue: queue, Clients: fakeClients{pusher}, Scheduler: scheduler, Clock: fake,
	}
	return service, config, queue, pusher, scheduler, fake
}

func TestRecord_QueuesOneRowPerServerThatTakesTheLibraryWithMappedPaths(t *testing.T) {
	service, _, queue, _, scheduler, fake := fixture()
	if err := service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/shows/Show A/Season 1"}); err != nil {
		t.Fatal(err)
	}
	want := []library.PendingPush{{
		ID: 1, ServerID: 1, CategoryID: 7, Path: "/data/shows/Show A/Season 1", Change: library.PendingCreated, WorkID: 5, QueuedAt: fake.Now().Unix(),
	}}
	if !reflect.DeepEqual(queue.rows, want) {
		t.Fatalf("rows = %+v, want %+v", queue.rows, want)
	}
	if !reflect.DeepEqual(scheduler.scheduled, []int64{1}) {
		t.Fatalf("scheduled = %v", scheduler.scheduled)
	}
}

func TestRecordWork_UsesFoldersOfNewFilesForACreatedChange(t *testing.T) {
	service, _, queue, _, _, fake := fixture()
	now := fake.Now().Unix()
	queue.files = []library.WorkFilePlace{
		{RootFolderID: 1, RelPath: "Show A/Season 1/e1.mkv", CreatedAt: now - 3600},
		{RootFolderID: 1, RelPath: "Show A/Season 2/e1.mkv", CreatedAt: now - 5},
	}
	if err := service.RecordWork(t.Context(), 7, 5, library.PendingCreated); err != nil {
		t.Fatal(err)
	}
	if len(queue.rows) != 1 || queue.rows[0].Path != "/data/shows/Show A/Season 2" {
		t.Fatalf("rows = %+v", queue.rows)
	}
}

func TestFlush_SendsTheDueRowsOnceAndDeletesThem(t *testing.T) {
	service, config, queue, pusher, _, fake := fixture()
	_ = service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/a"})
	fake.Advance(time.Minute)
	if err := service.Flush(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	want := [][]mediaserver.Change{{{Path: "/data/a", Kind: "created", LibraryIDs: []string{"lib-a"}}}}
	if !reflect.DeepEqual(pusher.sent, want) || len(queue.rows) != 0 || config.results[1] != "" {
		t.Fatalf("sent %+v rows %+v result %q", pusher.sent, queue.rows, config.results[1])
	}
}

func TestFlush_LeavesRowsInsideTheDebounceWindow(t *testing.T) {
	service, _, queue, pusher, _, _ := fixture()
	_ = service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/a"})
	if err := service.Flush(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if len(pusher.sent) != 0 || len(queue.rows) != 1 {
		t.Fatalf("sent %+v rows %+v", pusher.sent, queue.rows)
	}
}

func TestFlush_FailureCountsAnAttemptAndTheTenthDropsTheRow(t *testing.T) {
	service, config, queue, pusher, _, fake := fixture()
	pusher.err = errors.New("server down")
	_ = service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/a"})
	fake.Advance(time.Minute)
	for attempt := 1; attempt < serverpush.MaxAttempts; attempt++ {
		if err := service.Flush(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		if len(queue.rows) != 1 || queue.rows[0].Attempts != int64(attempt) || config.results[1] != "server down" {
			t.Fatalf("attempt %d: rows %+v result %q", attempt, queue.rows, config.results[1])
		}
	}
	if err := service.Flush(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if len(queue.rows) != 0 || config.results[1] != "1 paths dropped after 10 attempts: server down" {
		t.Fatalf("rows %+v result %q", queue.rows, config.results[1])
	}
}

func TestScheduleDue_AsksForEveryServerWithDueRows(t *testing.T) {
	service, _, _, _, scheduler, fake := fixture()
	_ = service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/a"})
	scheduler.scheduled = nil
	fake.Advance(time.Minute)
	if err := service.ScheduleDue(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(scheduler.scheduled, []int64{1}) {
		t.Fatalf("scheduled = %v", scheduler.scheduled)
	}
}

func TestFlush_AnnouncesEachNewWorkOnceWhenTheConnectionAsks(t *testing.T) {
	service, config, queue, _, _, fake := fixture()
	pusher := &announcingPusher{}
	service.Clients = fakeClients{pusher}
	config.servers[0].Announce = true
	if err := service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/shows/Show A/S1", "/media/shows/Show A/S2"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if err := service.Flush(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Now available: Show A"}; !reflect.DeepEqual(pusher.said, want) {
		t.Errorf("said = %q, want %q", pusher.said, want)
	}
	if len(queue.rows) != 0 {
		t.Errorf("rows left = %+v", queue.rows)
	}
}

func TestFlush_BusyServerKeepsRowsWithoutACountedAttempt(t *testing.T) {
	service, _, queue, pusher, _, fake := fixture()
	pusher.err = mediaserver.ErrBusy
	if err := service.Record(t.Context(), 7, 5, library.PendingCreated, []string{"/media/shows/Show A"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if err := service.Flush(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if len(queue.rows) != 1 || queue.rows[0].Attempts != 0 {
		t.Errorf("rows = %+v, want one untouched row", queue.rows)
	}
}
