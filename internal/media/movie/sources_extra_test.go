package movie_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
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
