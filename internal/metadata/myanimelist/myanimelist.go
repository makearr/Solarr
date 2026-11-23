// Package myanimelist reads MyAnimeList's API v2 with the owner's client id: an anime's score, synopsis and poster.
package myanimelist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	baseURL          = "https://api.myanimelist.net/v2"
	maxResponseBytes = 1 << 20
	animeFields      = "title,mean,num_scoring_users,synopsis,start_date,main_picture,media_type"
)

// ErrNotFound means MyAnimeList holds no anime under that id.
var ErrNotFound = errors.New("myanimelist: not found")

// ErrNoKey means no client id is stored, so nothing is asked.
var ErrNoKey = errors.New("myanimelist: no client id")

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks MyAnimeList with the owner's client id, read at each call.
type Client struct {
	http Doer
	key  func() string
}

// New builds a client.
func New(client Doer, key func() string) Client { return Client{http: client, key: key} }

// Configured says a client id is stored.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Anime is what MyAnimeList says of one anime.
type Anime struct {
	ID        int64
	Title     string
	Synopsis  string
	StartDate string
	// Score is the members' mean from 0 to 10 and Votes how many scored.
	Score  float64
	Votes  int64
	Poster string
}

// Anime reads one anime by its MyAnimeList id.
func (c Client) Anime(ctx context.Context, id int64) (Anime, error) {
	key := ""
	if c.key != nil {
		key = c.key()
	}
	if key == "" {
		return Anime{}, ErrNoKey
	}
	address := baseURL + "/anime/" + strconv.FormatInt(id, 10) + "?fields=" + url.QueryEscape(animeFields)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Anime{}, fmt.Errorf("myanimelist: build request: %w", err)
	}
	request.Header.Set("X-MAL-CLIENT-ID", key)
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) && status.Status == http.StatusNotFound {
			return Anime{}, ErrNotFound
		}
		return Anime{}, fmt.Errorf("myanimelist: anime %d: %w", id, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return Anime{}, fmt.Errorf("myanimelist: read: %w", err)
	}
	var wire struct {
		ID          int64   `json:"id"`
		Title       string  `json:"title"`
		Synopsis    string  `json:"synopsis"`
		StartDate   string  `json:"start_date"`
		Mean        float64 `json:"mean"`
		Scoring     int64   `json:"num_scoring_users"`
		MainPicture struct {
			Large  string `json:"large"`
			Medium string `json:"medium"`
		} `json:"main_picture"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return Anime{}, fmt.Errorf("myanimelist: decode: %w", err)
	}
	poster := wire.MainPicture.Large
	if poster == "" {
		poster = wire.MainPicture.Medium
	}
	return Anime{ID: wire.ID, Title: wire.Title, Synopsis: wire.Synopsis, StartDate: wire.StartDate, Score: wire.Mean, Votes: wire.Scoring, Poster: poster}, nil
}
