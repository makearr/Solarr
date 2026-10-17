package movie_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

func TestEachMoreProviderGivesWhatItIsBestAt(t *testing.T) {
	ratings := movie.FromMDBList(mdblist.Media{IMDbID: "tt9000001", Ratings: []mdblist.Rating{
		{Source: "tomatoes", Value: 97, Score: 97}, {Source: "imdb", Value: 8.1, Score: 81, Votes: 1200}, {Source: "letterboxd"}, {Source: "somesite", Score: 50},
	}})
	want := []movie.Rating{{Provider: "imdb", Score: 8.1, Votes: 1200}, {Provider: "rottentomatoes", Score: 9.7}}
	if !reflect.DeepEqual(ratings.Ratings, want) {
		t.Errorf("MDBList ratings = %+v, want %+v (no score, no rating; an unknown site is left out)", ratings.Ratings, want)
	}

	plot := movie.FromOMDb(omdb.Movie{Plot: "A long plot.", IMDbID: "tt9000001", IMDbVotes: "1,234", Ratings: []omdb.Rating{
		{Source: "Internet Movie Database", Value: "8.1/10"}, {Source: "Rotten Tomatoes", Value: "97%"}, {Source: "Metacritic", Value: "87/100"},
	}})
	if plot.Overview != "A long plot." || !reflect.DeepEqual(plot.Ratings, []movie.Rating{
		{Provider: "imdb", Score: 8.1, Votes: 1234}, {Provider: "rottentomatoes", Score: 9.7}, {Provider: "metacritic", Score: 8.7},
	}) {
		t.Errorf("OMDb = %+v", plot)
	}
	if movie.FromOMDb(omdb.Movie{Plot: "N/A"}).Overview != "" {
		t.Error("OMDb's N/A is no plot")
	}

	art := movie.FromFanart(fanart.Movie{Logos: []fanart.Image{
		{URL: "https://a/de.png", Lang: "de", Likes: "9"}, {URL: "https://a/en-less.png", Lang: "en", Likes: "1"}, {URL: "https://a/en.png", Lang: "en", Likes: "5"},
	}})
	if len(art.Images) != 1 || art.Images[0] != (movie.Image{Kind: "logo", URL: "https://a/en.png", Source: "fanart"}) {
		t.Errorf("fanart images = %+v; want the best liked English logo", art.Images)
	}

	record := tvdb.Movie{Year: "1999", Runtime: 124, Genres: []tvdb.Named{{Name: "Thriller"}},
		RemoteIDs: []tvdb.RemoteID{{ID: "tt9000001", SourceName: "IMDB"}, {ID: "7001", SourceName: "TheMovieDB.com"}},
		Artworks:  []tvdb.Artwork{{Image: "https://t/poster.jpg", Type: 14}, {Image: "https://t/poster-de.jpg", Type: 14, Language: "deu"}, {Image: "https://t/logo.png", Type: 25}}}
	record.Translations.Overviews = []tvdb.Translation{{Language: "deu", Overview: "Lang."}, {Language: "eng", Overview: "Long."}}
	fromTVDB := movie.FromTVDB(record)
	if fromTVDB.Overview != "Long." || fromTVDB.IDs != (movie.IDs{TMDB: 7001, IMDb: "tt9000001"}) || fromTVDB.Year != 1999 || len(fromTVDB.Images) != 2 {
		t.Errorf("TheTVDB = %+v", fromTVDB)
	}
}

type fakeWikidata struct{ facts map[string]wikidata.WorkFacts }

func (fakeWikidata) MovieIDs(context.Context, int64, string) (wikidata.WorkIDs, bool, error) {
	return wikidata.WorkIDs{}, false, nil
}

func (f fakeWikidata) FactsOf(_ context.Context, _ wikidata.FactKind, ids []string) (map[string]wikidata.WorkFacts, error) {
	out := map[string]wikidata.WorkFacts{}
	for _, id := range ids {
		if facts, ok := f.facts[id]; ok {
			out[id] = facts
		}
	}
	return out, nil
}

type fakeTVDBFilms struct{ movie tvdb.Movie }

func (f fakeTVDBFilms) Movie(context.Context, int64) (tvdb.Movie, error) { return f.movie, nil }

func (fakeTVDBFilms) MovieByRemoteID(context.Context, string) (int64, bool, error) {
	return 9, true, nil
}

func TestWikidataLeadsWhatItHoldsButNeverTheTitleAndItsGenresOnlyFill(t *testing.T) {
	facts := wikidata.WorkFacts{QID: "Q1", Summary: "Example Film is a 1999 film.", OriginalTitle: "Le Film Exemplaire", Date: "1999-06-20",
		RuntimeMin: 124, Languages: []string{"fr"}, Genres: []string{"Time-travel", "Thriller"}}
	service := movie.Service{
		Wikidata: fakeWikidata{facts: map[string]wikidata.WorkFacts{"7001": facts}},
		TVDB:     fakeTVDBFilms{movie: tvdb.Movie{Name: "Example Film", Year: "1998", Runtime: 120}},
		Priority: func() []string { return []string{"wikidata", "tvdb"} },
	}
	got, err := service.FetchFor(t.Context(), 0, movie.IDs{TMDB: 7001})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Example Film" || got.Overview != facts.Summary || got.OriginalTitle != "Le Film Exemplaire" || got.Year != 1998 || got.RuntimeMin != 124 || got.OriginalLanguage != "fr" {
		t.Errorf("movie = %+v; want Wikidata's facts first, TheTVDB's title and year", got)
	}
	if !reflect.DeepEqual(got.Genres, facts.Genres) {
		t.Errorf("genres = %v; want Wikidata's where no provider names any", got.Genres)
	}
	service.TVDB = fakeTVDBFilms{movie: tvdb.Movie{Name: "Example Film"}}
	if lone, _ := service.FetchFor(t.Context(), 0, movie.IDs{TMDB: 7001}); lone.Year != 1999 {
		t.Errorf("year = %d; want Wikidata's when no provider names one", lone.Year)
	}
	service.TVDB = fakeTVDBFilms{movie: tvdb.Movie{Name: "Example Film", Genres: []tvdb.Named{{Name: "Thriller"}}}}
	if again, _ := service.FetchFor(t.Context(), 0, movie.IDs{TMDB: 7001}); !reflect.DeepEqual(again.Genres, []string{"Thriller"}) {
		t.Errorf("genres = %v; want the provider's over Wikidata's finer ones", again.Genres)
	}
}
