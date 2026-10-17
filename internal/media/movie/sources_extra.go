package movie

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/fanart"
	"github.com/makearr/Solarr/internal/metadata/mdblist"
	"github.com/makearr/Solarr/internal/metadata/omdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// The providers beyond TMDB and Radarr's service, as the movie fetch asks them; each is nil until wired.
type (
	// RatingsSource is MDBList: every site's rating of a film in one request.
	RatingsSource interface {
		Configured() bool
		Movie(ctx context.Context, tmdb int64, imdb string) (mdblist.Media, error)
	}
	// PlotSource is OMDb: IMDb, Rotten Tomatoes and Metacritic ratings and an English plot.
	PlotSource interface {
		Configured() bool
		Movie(ctx context.Context, imdb string) (omdb.Movie, error)
	}
	// ArtSource is fanart.tv: clear logos, clear art, disc art, banners.
	ArtSource interface {
		Configured() bool
		Movie(ctx context.Context, tmdb int64, imdb string) (fanart.Movie, error)
	}
	// TVDBSource is TheTVDB's films.
	TVDBSource interface {
		Movie(ctx context.Context, id int64) (tvdb.Movie, error)
		MovieByRemoteID(ctx context.Context, remoteID string) (int64, bool, error)
	}
)

// mdblistProviders names MDBList's sources as the other providers name the same sites.
var mdblistProviders = map[string]string{
	"imdb": "imdb", "tmdb": "tmdb", "trakt": "trakt", "letterboxd": "letterboxd", "tomatoes": "rottentomatoes",
	"popcorn": "rottentomatoes_audience", "metacritic": "metacritic", "metacriticuser": "metacritic_users", "rogerebert": "rogerebert",
	"myanimelist": "myanimelist",
}

// FromMDBList is a film's ratings from every site MDBList reads, on a scale of 0 to 10.
func FromMDBList(in mdblist.Media) Movie {
	movie := Movie{IDs: IDs{IMDb: in.IMDbID}, Sources: []string{sourceMDBList}}
	for _, rating := range in.Ratings {
		provider, known := mdblistProviders[rating.Source]
		if !known || rating.Score <= 0 {
			continue
		}
		movie.Ratings = append(movie.Ratings, Rating{Provider: provider, Score: rating.Score / 10, Votes: rating.Votes})
	}
	slices.SortFunc(movie.Ratings, func(a, b Rating) int { return cmp.Compare(a.Provider, b.Provider) })
	return movie
}

// FromOMDb is a film's English plot and its IMDb, Rotten Tomatoes and Metacritic ratings, on a scale of 0 to 10.
func FromOMDb(in omdb.Movie) Movie {
	movie := Movie{IDs: IDs{IMDb: in.IMDbID}, Sources: []string{sourceOMDb}}
	if in.Plot != "N/A" {
		movie.Overview = in.Plot
	}
	votes, _ := strconv.ParseInt(strings.ReplaceAll(in.IMDbVotes, ",", ""), 10, 64)
	for _, rating := range in.Ratings {
		value := strings.TrimSpace(rating.Value)
		var score float64
		switch {
		case strings.HasSuffix(value, "/10"):
			score, _ = strconv.ParseFloat(strings.TrimSuffix(value, "/10"), 64)
		case strings.HasSuffix(value, "/100"):
			score, _ = strconv.ParseFloat(strings.TrimSuffix(value, "/100"), 64)
			score /= 10
		case strings.HasSuffix(value, "%"):
			score, _ = strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
			score /= 10
		}
		if score <= 0 {
			continue
		}
		switch rating.Source {
		case "Internet Movie Database":
			movie.Ratings = append(movie.Ratings, Rating{Provider: "imdb", Score: score, Votes: votes})
		case "Rotten Tomatoes":
			movie.Ratings = append(movie.Ratings, Rating{Provider: "rottentomatoes", Score: score})
		case "Metacritic":
			movie.Ratings = append(movie.Ratings, Rating{Provider: "metacritic", Score: score})
		}
	}
	return movie
}

// FromFanart is the best liked picture of each kind fanart.tv has of a film, English or without text first.
func FromFanart(in fanart.Movie) Movie {
	movie := Movie{Sources: []string{sourceFanart}}
	for _, kind := range []struct {
		name   string
		images []fanart.Image
	}{
		{"logo", in.Logos}, {"clearart", in.ClearArt}, {"disc", in.Discs}, {"banner", in.Banners},
		{"poster", in.Posters}, {"fanart", in.Backgrounds}, {"thumb", in.Thumbs},
	} {
		if best, ok := bestFanart(kind.images); ok {
			movie.Images = append(movie.Images, Image{Kind: kind.name, URL: best.URL, Source: sourceFanart})
		}
	}
	return movie
}

func bestFanart(images []fanart.Image) (fanart.Image, bool) {
	rank := func(image fanart.Image) int {
		switch image.Lang {
		case "en":
			return 0
		case "", "00":
			return 1
		}
		return 2
	}
	sorted := slices.Clone(images)
	slices.SortStableFunc(sorted, func(a, b fanart.Image) int {
		likesA, _ := strconv.Atoi(a.Likes)
		likesB, _ := strconv.Atoi(b.Likes)
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(likesB, likesA))
	})
	if len(sorted) == 0 || sorted[0].URL == "" {
		return fanart.Image{}, false
	}
	return sorted[0], true
}

// tvdbArtworkKinds are TheTVDB's numbered picture kinds of a film, as the others name them.
var tvdbArtworkKinds = map[int]string{14: "poster", 15: "fanart", 16: "banner", 22: "clearart", 25: "logo"}

// FromTVDB is a film as TheTVDB has it: its English overview, genres, runtime, pictures and the ids it knows.
func FromTVDB(in tvdb.Movie) Movie {
	movie := Movie{Title: in.Name, Overview: in.Overview("eng"), RuntimeMin: in.Runtime, Sources: []string{sourceTVDB}}
	movie.IDs.IMDb = in.RemoteID("IMDB")
	movie.IDs.TMDB, _ = strconv.ParseInt(in.RemoteID("TheMovieDB.com"), 10, 64)
	movie.Year, _ = strconv.Atoi(in.Year)
	for _, genre := range in.Genres {
		movie.Genres = append(movie.Genres, genre.Name)
	}
	seen := map[string]bool{}
	for _, artwork := range in.Artworks {
		kind, known := tvdbArtworkKinds[artwork.Type]
		if !known || seen[kind] || artwork.Language != "" && artwork.Language != "eng" {
			continue
		}
		seen[kind] = true
		movie.Images = append(movie.Images, Image{Kind: kind, URL: artwork.Image, Source: sourceTVDB})
	}
	return movie
}

// FromWikidata is what Wikidata's item and the English Wikipedia article say of a film: its overview (the article's
// lead), original title, year, runtime and language. Its title stays the identifying provider's, which names folders
// and matches releases; its genres are too fine ("time-travel film") to lead, so they only fill where no one else has any.
func FromWikidata(in wikidata.WorkFacts) Movie {
	movie := Movie{Overview: in.Summary, OriginalTitle: in.OriginalTitle, RuntimeMin: in.RuntimeMin, spareGenres: in.Genres, Sources: []string{sourceWikidata}}
	if len(in.Date) >= 4 {
		movie.Year, _ = strconv.Atoi(in.Date[:4])
	}
	if len(in.Languages) > 0 {
		movie.OriginalLanguage = in.Languages[0]
	}
	return movie
}
