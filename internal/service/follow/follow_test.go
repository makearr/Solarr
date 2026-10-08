package follow_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/follow"
	"github.com/makearr/Solarr/migrations"
)

// fakeType is a media type whose follows find a fixed list and record what they add and search.
type fakeType struct {
	key        media.Key
	candidates []media.FollowCandidate
	limit      int
	added      *[]string
	searched   *[]int64
	seen       *[]media.Follow
}

func (f fakeType) Descriptor() media.Descriptor {
	return media.Descriptor{Key: f.key, FollowOptions: []media.FollowOption{
		{Key: "monitor", Choices: []media.Choice{{Key: "all"}, {Key: "future"}}},
		{Key: "kinds", Many: true, Choices: []media.Choice{{Key: "a"}, {Key: "b"}}},
	}}
}

func (f fakeType) FollowCandidates(_ context.Context, followed media.Follow) ([]media.FollowCandidate, error) {
	*f.seen = append(*f.seen, followed)
	return f.candidates, nil
}

func (f fakeType) AddFollowed(_ context.Context, _ media.Follow, candidate media.FollowCandidate) error {
	*f.added = append(*f.added, candidate.ExternalID)
	return nil
}

func (f fakeType) FollowLimit(bool) int { return f.limit }

func (f fakeType) SearchFollowed(_ context.Context, followed media.Follow) error {
	*f.searched = append(*f.searched, followed.SubjectID)
	return nil
}

// plainType declares a media type with nothing to follow.
type plainType struct{}

func (plainType) Descriptor() media.Descriptor { return media.Descriptor{Key: "plain"} }

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

type fixture struct {
	service  follow.Service
	added    []string
	searched []int64
	seen     []media.Follow
	subject  library.Subject
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	opened := openLibrary(t)
	f := &fixture{}
	module := fakeType{
		key: "fake", limit: 2, added: &f.added, searched: &f.searched, seen: &f.seen,
		candidates: []media.FollowCandidate{
			{Provider: "p", ExternalID: "held", Held: true},
			{Provider: "p", ExternalID: "excluded"},
			{Provider: "p", ExternalID: "c"}, {Provider: "p", ExternalID: "d"}, {Provider: "p", ExternalID: "e"},
		},
	}
	registry, err := media.NewRegistry(module, plainType{})
	if err != nil {
		t.Fatal(err)
	}
	if err := library.NewExclusions(opened).Exclude(t.Context(), time.Unix(1, 0), "fake", "Gone",
		[]library.ExternalIDWrite{{Provider: "p", ExternalID: "excluded"}}); err != nil {
		t.Fatal(err)
	}
	f.subject = library.Subject{Type: "person", ID: 42}
	f.service = follow.Service{
		Subscriptions: library.NewSubscriptions(opened), Exclusions: library.NewExclusions(opened), Media: registry,
		Libraries: libraries{1: {ID: 1, MediaType: "fake"}, 2: {ID: 2, MediaType: "plain"}},
		Clock:     clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler),
	}
	return f
}

func TestPreview_SaysWhatTheNextRunAddsAndWhatItSkips(t *testing.T) {
	f := newFixture(t)
	preview, err := f.service.Preview(t.Context(), f.subject, follow.Request{CategoryID: 1, Options: map[string][]string{"monitor": {"all"}}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, candidate := range preview.WouldAdd {
		ids = append(ids, candidate.ExternalID)
	}
	if !slices.Equal(ids, []string{"c", "d"}) || preview.More != 1 || preview.Held != 2 || preview.Limit != 2 {
		t.Errorf("preview = %v, more %d, held %d, limit %d; want c and d, 1 more, 2 held, limit 2", ids, preview.More, preview.Held, preview.Limit)
	}
	if len(f.added) != 0 {
		t.Errorf("a preview added %v", f.added)
	}
	if got := f.seen[0].FiltersJSON; got != `{"monitor":"all"}` {
		t.Errorf("the module saw options %s, want the one choice stored as a value", got)
	}
}

func TestFollow_RunAddsUpToTheLimitAndSearchesWhenAsked(t *testing.T) {
	f := newFixture(t)
	followed, err := f.service.Follow(t.Context(), f.subject, 7, follow.Request{
		CategoryID: 1, Roles: []string{"actor"}, Options: map[string][]string{"kinds": {"a", "b"}}, Search: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if followed.CategoryID != 1 || !slices.Equal(followed.MediaTypes, []string{"fake"}) || followed.FiltersJSON != `{"kinds":["a","b"]}` {
		t.Errorf("follow = %+v; want it in library 1, for the library's type, with the options", followed)
	}
	payload, _ := json.Marshal(follow.Payload{SubscriptionID: followed.ID, Search: true})
	if err := f.service.Run(t.Context(), jobs.Job{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.added, []string{"c", "d"}) || !slices.Equal(f.searched, []int64{42}) {
		t.Errorf("added %v, searched %v; want c and d, then a search for the person", f.added, f.searched)
	}
}

func TestFollow_RefusesWhatTheLibraryCannotTake(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		request follow.Request
		want    error
	}{
		{follow.Request{CategoryID: 9}, follow.ErrNoLibrary},
		{follow.Request{CategoryID: 2}, follow.ErrNotFollowable},
		{follow.Request{CategoryID: 1, Options: map[string][]string{"monitor": {"sometimes"}}}, follow.ErrInvalidOption},
	} {
		if _, err := f.service.Follow(t.Context(), f.subject, 7, tc.request); !errors.Is(err, tc.want) {
			t.Errorf("%+v: err = %v, want %v", tc.request, err, tc.want)
		}
	}
}
