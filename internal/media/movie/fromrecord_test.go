package movie

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

type fakeRecords struct{ asked [][2]string }

func (f *fakeRecords) ByID(_ context.Context, namespace, id string) (specprovider.Record, error) {
	f.asked = append(f.asked, [2]string{namespace, id})
	if namespace != "imdb" {
		return specprovider.Record{}, context.Canceled
	}
	return specprovider.Record{
		IDs: map[string]string{"imdb": id, "own": "9"}, Title: "The Lantern Keeper", OriginalTitle: "La Gardienne", Year: 2019, Overview: "A keeper of lanterns.",
		Language: "fr", RuntimeSeconds: 6000, AgeRating: "PG", Genres: []string{"Drama"}, Date: "2019-05-01T00:00:00Z",
		People:        []specprovider.Person{{Name: "Ines Vale", Role: "director"}, {Name: "Mara Quell", Role: "actor"}},
		Organisations: []specprovider.Organisation{{Name: "Lantern Films"}},
		Images:        []specprovider.Image{{Kind: "backdrop", URL: "https://img.example.org/b.jpg"}},
	}, nil
}

func TestFromRecordFillsAMovie(t *testing.T) {
	provider := &fakeRecords{}
	record, _ := provider.ByID(t.Context(), "imdb", "tt0000001")
	got := FromRecord("custom_films", record)
	if got.Title != "The Lantern Keeper" || got.RuntimeMin != 100 || got.IDs.IMDb != "tt0000001" || got.Premiere != "2019-05-01" || got.Studio != "Lantern Films" ||
		got.Certification != "PG" || got.OriginalLanguage != "fr" || got.Images[0].Kind != "fanart" || got.Images[0].Source != "custom_films" {
		t.Fatalf("movie = %+v", got)
	}
	if got.Credits[0].Role != RoleDirector || got.Credits[1].Role != RoleActor || len(got.Aliases) != 1 || got.Aliases[0] != "La Gardienne" {
		t.Errorf("credits %+v aliases %v", got.Credits, got.Aliases)
	}
}

func TestAskRecordTriesImdbThenTmdbAndSkipsAMovieWithNeither(t *testing.T) {
	provider := &fakeRecords{}
	service := Service{Records: func(key string) RecordProvider {
		if key == "custom_films" {
			return provider
		}
		return nil
	}}
	if _, ok := service.askRecord(t.Context(), "custom_films", IDs{TMDB: 5, IMDb: "tt0000001"}); !ok {
		t.Fatal("want an answer by the IMDb id")
	}
	if len(provider.asked) != 1 || provider.asked[0] != [2]string{"imdb", "tt0000001"} {
		t.Errorf("asked %v, want only IMDb", provider.asked)
	}
	provider.asked = nil
	if _, ok := service.askRecord(t.Context(), "custom_films", IDs{TMDB: 5}); ok || len(provider.asked) != 1 || provider.asked[0][0] != "tmdb" {
		t.Errorf("a movie with only a TMDB id: asked %v", provider.asked)
	}
	if _, ok := service.askRecord(t.Context(), "custom_films", IDs{}); ok {
		t.Error("a movie with no id was asked")
	}
	if _, ok := service.askRecord(t.Context(), "gone_definition", IDs{IMDb: "tt1"}); ok {
		t.Error("a gone definition answered")
	}
}
