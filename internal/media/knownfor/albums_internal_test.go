package knownfor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

type fakeAlbums struct{ asked []string }

func (f *fakeAlbums) ArtistFor(_ context.Context, qid string) (string, error) {
	f.asked = append(f.asked, "qid:"+qid)
	if qid == "Q7" {
		return "artist-from-wikidata", nil
	}
	return "", nil
}

func (f *fakeAlbums) Albums(_ context.Context, artist string) ([]media.KnownFor, error) {
	f.asked = append(f.asked, "albums:"+artist)
	return []media.KnownFor{{ExternalID: "rg-" + artist, Kind: "album"}}, nil
}

func openLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
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
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

func TestAPersonsAlbumsComeFromTheirOwnArtistIDElseWikidatas(t *testing.T) {
	opened := openLibrary(t)
	people := library.NewPeople(opened)
	now := clock.NewFakeAtEpoch().Now()
	artist, err := people.ResolvePerson(t.Context(), now, library.PersonLink{
		Provider: musicBrainzArtistProvider, ExternalID: "artist-held", Person: library.PersonIdentity{Name: "Vela Orrin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := people.ResolvePerson(t.Context(), now, library.PersonLink{
		Provider: "tmdb", ExternalID: "77", Person: library.PersonIdentity{Name: "Tamsin Rue"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		personID  int64
		qid       string
		wantAsked []string
		wantFound int
	}{
		{"an artist the library holds", artist, "Q7", []string{"albums:artist-held"}, 1},
		{"an actor Wikidata knows as an artist", actor, "Q7", []string{"qid:Q7", "albums:artist-from-wikidata"}, 1},
		{"an actor who records nothing", actor, "Q8", []string{"qid:Q8"}, 0},
		{"an actor without a Wikidata item", actor, "", nil, 0},
	}
	for _, c := range cases {
		source := &fakeAlbums{}
		got := Profiling{People: people, Albums: source}.albums(t.Context(), c.personID, c.qid)
		if len(got) != c.wantFound || !reflect.DeepEqual(source.asked, c.wantAsked) {
			t.Errorf("%s: %d albums, asked %v; want %d, asked %v", c.name, len(got), source.asked, c.wantFound, c.wantAsked)
		}
	}
	if got := (Profiling{People: people}).albums(t.Context(), artist, "Q7"); got != nil {
		t.Errorf("no album source: %+v, want none", got)
	}
}
