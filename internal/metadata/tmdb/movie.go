package tmdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Collection is the franchise a movie belongs to, as the movie answer names it.
type Collection struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

// MovieCast is one cast entry of a movie.
type MovieCast struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

// MovieCrew is one crew entry of a movie.
type MovieCrew struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

// Release types of a movie's release dates.
const (
	ReleasePremiere        = 1
	ReleaseTheatricalLimit = 2
	ReleaseTheatrical      = 3
	ReleaseDigital         = 4
	ReleasePhysical        = 5
	ReleaseTV              = 6
)

// ReleaseDate is one dated release of a movie in a country.
type ReleaseDate struct {
	Certification string `json:"certification"`
	Type          int    `json:"type"`
	Date          string `json:"release_date"`
	Note          string `json:"note"`
}

// Movie is one film with everything the appended answers carry.
type Movie struct {
	ID               int64   `json:"id"`
	IMDbID           string  `json:"imdb_id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	Tagline          string  `json:"tagline"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	Runtime          int     `json:"runtime"`
	Status           string  `json:"status"`
	OriginalLanguage string  `json:"original_language"`
	Popularity       float64 `json:"popularity"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	Genres           []Genre `json:"genres"`
	Companies        []struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		LogoPath      string `json:"logo_path"`
		OriginCountry string `json:"origin_country"`
	} `json:"production_companies"`
	Countries []struct {
		Code string `json:"iso_3166_1"`
		Name string `json:"name"`
	} `json:"production_countries"`
	Collection *Collection `json:"belongs_to_collection"`
	Credits    struct {
		Cast []MovieCast `json:"cast"`
		Crew []MovieCrew `json:"crew"`
	} `json:"credits"`
	ReleaseDates struct {
		Results []struct {
			Country string        `json:"iso_3166_1"`
			Dates   []ReleaseDate `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	Keywords struct {
		Keywords []Genre `json:"keywords"`
	} `json:"keywords"`
	Images struct {
		Posters   []Image `json:"posters"`
		Backdrops []Image `json:"backdrops"`
		Logos     []Image `json:"logos"`
	} `json:"images"`
	AlternativeTitles struct {
		Titles []struct {
			Title   string `json:"title"`
			Country string `json:"iso_3166_1"`
		} `json:"titles"`
	} `json:"alternative_titles"`
}

// Certification answers the movie's certification for a country, "US" when asked for none, else the first listed.
func (m Movie) Certification(country string) string {
	for _, wanted := range []string{country, "US"} {
		for _, entry := range m.ReleaseDates.Results {
			if wanted == "" || entry.Country != wanted {
				continue
			}
			for _, date := range entry.Dates {
				if date.Certification != "" {
					return date.Certification
				}
			}
		}
	}
	for _, entry := range m.ReleaseDates.Results {
		for _, date := range entry.Dates {
			if date.Certification != "" {
				return date.Certification
			}
		}
	}
	return ""
}

// ReleaseOfType is the earliest date (yyyy-MM-dd) of a release type in any country, "" when the movie has none.
func (m Movie) ReleaseOfType(kinds ...int) string {
	earliest := ""
	for _, entry := range m.ReleaseDates.Results {
		for _, date := range entry.Dates {
			day, _, _ := strings.Cut(date.Date, "T")
			if day == "" || !containsKind(kinds, date.Type) {
				continue
			}
			if earliest == "" || day < earliest {
				earliest = day
			}
		}
	}
	return earliest
}

func containsKind(kinds []int, kind int) bool {
	for _, wanted := range kinds {
		if wanted == kind {
			return true
		}
	}
	return false
}

const movieAppend = "credits,external_ids,release_dates,keywords,images,alternative_titles"

// Movie reads one film by TMDB id with its credits, release dates, keywords, artwork and titles.
func (c Client) Movie(ctx context.Context, id int64) (Movie, error) {
	var movie Movie
	query := url.Values{"append_to_response": {movieAppend}, "include_image_language": {c.imageLanguages()}}
	return movie, c.get(ctx, "/movie/"+strconv.FormatInt(id, 10), query, &movie)
}

// MovieHit is one film a search found.
type MovieHit struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	OriginalTitle    string `json:"original_title"`
	Overview         string `json:"overview"`
	ReleaseDate      string `json:"release_date"`
	PosterPath       string `json:"poster_path"`
	OriginalLanguage string `json:"original_language"`
}

// SearchMovies finds films by name, narrowed to a release year when one is given.
func (c Client) SearchMovies(ctx context.Context, term string, year int) ([]MovieHit, error) {
	var answer struct {
		Results []MovieHit `json:"results"`
	}
	query := url.Values{"query": {term}}
	if year > 0 {
		query.Set("year", strconv.Itoa(year))
	}
	return answer.Results, c.get(ctx, "/search/movie", query, &answer)
}

// FindMovieByIMDb resolves an IMDb id (tt...) to TMDB's film; ErrNotFound when TMDB maps none.
func (c Client) FindMovieByIMDb(ctx context.Context, imdbID string) (MovieHit, error) {
	var answer struct {
		Results []MovieHit `json:"movie_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), url.Values{"external_source": {"imdb_id"}}, &answer); err != nil {
		return MovieHit{}, err
	}
	if len(answer.Results) == 0 {
		return MovieHit{}, ErrNotFound
	}
	return answer.Results[0], nil
}

// CollectionDetail is a franchise with its films.
type CollectionDetail struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Overview     string     `json:"overview"`
	PosterPath   string     `json:"poster_path"`
	BackdropPath string     `json:"backdrop_path"`
	Parts        []MovieHit `json:"parts"`
}

// Collection reads one franchise by TMDB id.
func (c Client) Collection(ctx context.Context, id int64) (CollectionDetail, error) {
	var collection CollectionDetail
	return collection, c.get(ctx, "/collection/"+strconv.FormatInt(id, 10), nil, &collection)
}
