// Package omdb reads OMDb (omdbapi.com): a film's IMDb, Rotten Tomatoes and Metacritic ratings and its English plot. It
// needs the owner's free key, 1,000 requests a day.
package omdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotFound means OMDb knows no film under the id.
var ErrNotFound = errors.New("omdb: not found")

const (
	baseURL          = "https://www.omdbapi.com/"
	maxResponseBytes = 1 << 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks OMDb with the owner's key.
type Client struct {
	http Getter
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none.
func New(client Getter, key func() string) Client {
	return Client{http: client, key: key, base: baseURL}
}

// Configured says a key is stored, without which OMDb answers nothing.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Rating is one site's rating as OMDb writes it: "8.1/10", "97%", "87/100".
type Rating struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// Movie is a film as OMDb describes it; "N/A" stands for what it does not know.
type Movie struct {
	Title      string   `json:"Title"`
	Year       string   `json:"Year"`
	Rated      string   `json:"Rated"`
	Plot       string   `json:"Plot"`
	IMDbID     string   `json:"imdbID"`
	IMDbVotes  string   `json:"imdbVotes"`
	Ratings    []Rating `json:"Ratings"`
	Type       string   `json:"Type"`
	Seasons    string   `json:"totalSeasons"`
	IMDbRating string   `json:"imdbRating"`
	Response   string   `json:"Response"`
	ErrorShown string   `json:"Error"`
}

// Movie reads a film by its IMDb id, with the full plot.
func (c Client) Movie(ctx context.Context, imdb string) (Movie, error) {
	if !c.Configured() || imdb == "" {
		return Movie{}, ErrNotFound
	}
	var movie Movie
	query := url.Values{"apikey": {c.key()}, "i": {imdb}, "plot": {"full"}}
	if err := c.http.GetJSON(ctx, c.base+"?"+query.Encode(), maxResponseBytes, &movie); err != nil {
		return Movie{}, fmt.Errorf("omdb: %w", err)
	}
	if movie.Response != "True" {
		if strings.Contains(strings.ToLower(movie.ErrorShown), "not found") {
			return Movie{}, ErrNotFound
		}
		return Movie{}, fmt.Errorf("omdb: %s", movie.ErrorShown)
	}
	return movie, nil
}

// Episode is one episode of a season with its IMDb rating as OMDb writes it ("N/A" where none).
type Episode struct {
	Title      string `json:"Title"`
	Released   string `json:"Released"`
	Episode    string `json:"Episode"`
	IMDbRating string `json:"imdbRating"`
	IMDbID     string `json:"imdbID"`
}

// Season is one season of a series as OMDb lists it.
type Season struct {
	Number   string    `json:"Season"`
	Episodes []Episode `json:"Episodes"`
	Response string    `json:"Response"`
	Error    string    `json:"Error"`
}

// Season reads one season's episodes by the series' IMDb id: one request, only on a refresh.
func (c Client) Season(ctx context.Context, imdb string, number int) (Season, error) {
	if !c.Configured() || imdb == "" || number < 0 {
		return Season{}, ErrNotFound
	}
	var season Season
	query := url.Values{"apikey": {c.key()}, "i": {imdb}, "Season": {strconv.Itoa(number)}}
	if err := c.http.GetJSON(ctx, c.base+"?"+query.Encode(), maxResponseBytes, &season); err != nil {
		return Season{}, fmt.Errorf("omdb: %w", err)
	}
	if season.Response != "True" {
		if strings.Contains(strings.ToLower(season.Error), "not found") {
			return Season{}, ErrNotFound
		}
		return Season{}, fmt.Errorf("omdb: %s", season.Error)
	}
	return season, nil
}
