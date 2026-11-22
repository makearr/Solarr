package playback_test

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
	"github.com/makearr/Solarr/internal/service/playback"
)

type fakeSettings struct {
	server settings.MediaServer
	users  []settings.MediaServerUser
	result string
}

func (f *fakeSettings) MediaServers(context.Context) ([]settings.MediaServer, error) {
	return []settings.MediaServer{f.server}, nil
}
func (f *fakeSettings) MediaServerPaths(context.Context, int64) ([]settings.MediaServerPath, error) {
	return []settings.MediaServerPath{{SolarrPrefix: "/library/tv", ServerPrefix: "/media/tv"}}, nil
}
func (f *fakeSettings) MediaServerUsers(context.Context, int64) ([]settings.MediaServerUser, error) {
	return f.users, nil
}
func (f *fakeSettings) SetMediaServerUsers(_ context.Context, _ int64, users []settings.MediaServerUser) error {
	f.users = users
	return nil
}
func (f *fakeSettings) SetMediaServerResult(_ context.Context, _, _ int64, message string) error {
	f.result, f.server.LastError = message, message
	return nil
}
func (f *fakeSettings) MediaServerPlayedPercent() int64 { return 0 }
func (f *fakeSettings) RootFolders() []settings.RootFolder {
	return []settings.RootFolder{{ID: 7, Path: "/library/tv"}}
}

type fakeStore struct {
	files    map[string]library.PlaybackHit
	external map[string]library.PlaybackHit
	states   []library.PlaybackState
	since    map[string]int64
	failNext bool
}

func (f *fakeStore) ResolveFile(_ context.Context, root int64, rel string) (library.PlaybackHit, bool, error) {
	hit, ok := f.files[rel]
	return hit, ok && root == 7, nil
}
func (f *fakeStore) ResolveExternal(_ context.Context, provider, id string) (library.PlaybackHit, bool, error) {
	hit, ok := f.external[provider+":"+id]
	return hit, ok, nil
}
func (f *fakeStore) Upsert(_ context.Context, states []library.PlaybackState) error {
	if f.failNext {
		return errors.New("disk full")
	}
	f.states = append(f.states, states...)
	return nil
}
func (f *fakeStore) Since(_ context.Context, _ int64, user string) (int64, error) {
	return f.since[user], nil
}
func (f *fakeStore) SetSince(_ context.Context, _ int64, user string, since int64) error {
	f.since[user] = since
	return nil
}

type fakeReader struct {
	plays []mediaserver.Play
	asked []int64
}

func (f *fakeReader) Played(_ context.Context, _ string, since int64, _, _ int) ([]mediaserver.Play, bool, error) {
	f.asked = append(f.asked, since)
	return f.plays, true, nil
}

type fakeLister struct{}

func (fakeLister) Users(context.Context) ([]mediaserver.User, error) {
	return []mediaserver.User{{ID: "u1", Name: "Ada"}}, nil
}

type fakeClients struct{ client playback.Client }

func (f fakeClients) Playback(context.Context, settings.MediaServer) (playback.Client, error) {
	return f.client, nil
}

func setup(plays []mediaserver.Play) (playback.Service, *fakeSettings, *fakeStore, *fakeReader) {
	reader := &fakeReader{plays: plays}
	store := &fakeStore{
		files:    map[string]library.PlaybackHit{"Show/s01e01.mkv": {WorkID: 1, PartID: 11}},
		external: map[string]library.PlaybackHit{"tmdb:99": {WorkID: 2}},
		since:    map[string]int64{},
	}
	config := &fakeSettings{server: settings.MediaServer{ID: 3, Enabled: true, SyncPlayback: true}}
	service := playback.Service{
		Settings: config, Store: store, Clients: fakeClients{playback.Client{Reader: reader, Lister: fakeLister{}}},
		Clock: clock.NewFake(time.Unix(5000, 0)),
	}
	return service, config, store, reader
}

func TestSyncResolvesByPathThenIDAndSkipsMisses(t *testing.T) {
	service, config, store, _ := setup([]mediaserver.Play{
		{Path: "/media/tv/Show/s01e01.mkv", Played: true, PlayCount: 2, LastPlayed: 1000},
		{Path: "/elsewhere/x.mkv", ProviderIDs: map[string]string{"tmdb": "99"}, Played: true, LastPlayed: 900},
		{Path: "/media/tv/Gone.mkv", LastPlayed: 800},
	})
	outcome, err := service.Sync(context.Background(), 3, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []library.PlaybackState{
		{ServerID: 3, ServerUserID: "u1", WorkID: 1, PartID: 11, Played: true, PlayCount: 2, LastPlayedAt: 1000, UpdatedAt: 5000},
		{ServerID: 3, ServerUserID: "u1", WorkID: 2, Played: true, LastPlayedAt: 900, UpdatedAt: 5000},
	}
	if !reflect.DeepEqual(store.states, want) || outcome != (playback.Outcome{Stored: 2, Unresolved: 1}) {
		t.Fatalf("states %+v outcome %+v", store.states, outcome)
	}
	if len(config.users) != 1 || !config.users[0].Counted || config.users[0].UserID != nil {
		t.Fatalf("new users start counted and unmapped: %+v", config.users)
	}
}

func TestSyncCountsAPositionPastTheThresholdAsPlayed(t *testing.T) {
	service, _, store, _ := setup([]mediaserver.Play{
		{Path: "/media/tv/Show/s01e01.mkv", PositionMS: 91, DurationMS: 100, LastPlayed: 10},
	})
	if _, err := service.Sync(context.Background(), 3, false); err != nil {
		t.Fatal(err)
	}
	if !store.states[0].Played {
		t.Fatalf("91%% is played: %+v", store.states[0])
	}
}

func TestSyncMovesTheMarkOnlyAfterTheStoreSucceeded(t *testing.T) {
	service, config, store, reader := setup([]mediaserver.Play{{Path: "/media/tv/Show/s01e01.mkv", Played: true, LastPlayed: 1000}})
	store.failNext = true
	if _, err := service.Sync(context.Background(), 3, false); err == nil {
		t.Fatal("a failed store must fail the sync")
	}
	if store.since["u1"] != 0 || config.result == "" {
		t.Fatalf("mark %d, result %q", store.since["u1"], config.result)
	}
	store.failNext = false
	if _, err := service.Sync(context.Background(), 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(context.Background(), 3, false); err != nil {
		t.Fatal(err)
	}
	if store.since["u1"] != 1000 || !reflect.DeepEqual(reader.asked, []int64{0, 0, 1000}) || config.result != "" {
		t.Fatalf("mark %d asked %v result %q", store.since["u1"], reader.asked, config.result)
	}
}

func TestSyncFullAsksForEverythingAgain(t *testing.T) {
	service, _, store, reader := setup([]mediaserver.Play{{Path: "/media/tv/Show/s01e01.mkv", Played: true, LastPlayed: 1000}})
	if _, err := service.Sync(context.Background(), 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(context.Background(), 3, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.asked, []int64{0, 0}) || store.since["u1"] != 1000 {
		t.Fatalf("asked %v mark %d", reader.asked, store.since["u1"])
	}
}

func TestSyncSkipsAConnectionWithTheSwitchOff(t *testing.T) {
	service, config, store, reader := setup(nil)
	config.server.SyncPlayback = false
	if _, err := service.Sync(context.Background(), 3, false); err != nil || len(reader.asked) != 0 || len(store.states) != 0 {
		t.Fatalf("err %v asked %v", err, reader.asked)
	}
}
