// Package spotify reads Spotify's catalogue with the owner's app credentials (client credentials flow): an album's popularity and cover.
package spotify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	apiURL           = "https://api.spotify.com/v1"
	tokenURL         = "https://accounts.spotify.com/api/token"
	maxResponseBytes = 1 << 20
	// tokenMargin renews a token a little before it lapses.
	tokenMargin = 60 * time.Second
)

// ErrNotFound means Spotify holds no album of that artist and title.
var ErrNotFound = errors.New("spotify: not found")

// ErrNoKey means no credentials are stored, so nothing is asked.
var ErrNoKey = errors.New("spotify: no credentials")

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks Spotify. The credentials are one "client id:client secret" value, read at each call.
type Client struct {
	http  Doer
	key   func() string
	now   func() time.Time
	mu    *sync.Mutex
	token *token
}

type token struct {
	value   string
	expires time.Time
	owner   string
}

// New builds a client; now is the clock the token's lifetime is measured on.
func New(client Doer, key func() string, now func() time.Time) Client {
	return Client{http: client, key: key, now: now, mu: &sync.Mutex{}, token: &token{}}
}

// Configured says credentials are stored.
func (c Client) Configured() bool { return c.key != nil && strings.Contains(c.key(), ":") }

// Album is what Spotify says of one album.
type Album struct {
	ID string
	// Popularity is Spotify's own measure from 0 to 100.
	Popularity int
	Cover      string
	UPC        string
}

func (c Client) bearer(ctx context.Context) (string, error) {
	credentials := ""
	if c.key != nil {
		credentials = c.key()
	}
	if !strings.Contains(credentials, ":") {
		return "", ErrNoKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token.owner == credentials && c.now().Before(c.token.expires) {
		return c.token.value, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", fmt.Errorf("spotify: build token request: %w", err)
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var granted struct {
		Token   string `json:"access_token"`
		Seconds int64  `json:"expires_in"`
	}
	if err := c.read(ctx, request, &granted); err != nil {
		return "", fmt.Errorf("spotify: token: %w", err)
	}
	*c.token = token{value: granted.Token, expires: c.now().Add(time.Duration(granted.Seconds)*time.Second - tokenMargin), owner: credentials}
	return granted.Token, nil
}

func (c Client) read(ctx context.Context, request *http.Request, into any) error {
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

func (c Client) get(ctx context.Context, address string, into any) error {
	bearer, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("spotify: build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	return c.read(ctx, request, into)
}

// FindAlbum searches the album by exact artist and title and reads its page for the popularity.
func (c Client) FindAlbum(ctx context.Context, artist, title string) (Album, error) {
	var found struct {
		Albums struct {
			Items []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
			} `json:"items"`
		} `json:"albums"`
	}
	query := fmt.Sprintf(`artist:%q album:%q`, artist, title)
	if err := c.get(ctx, apiURL+"/search?type=album&limit=5&q="+url.QueryEscape(query), &found); err != nil {
		return Album{}, wrap("search", err)
	}
	for _, hit := range found.Albums.Items {
		if !strings.EqualFold(hit.Name, title) || len(hit.Artists) == 0 || !strings.EqualFold(hit.Artists[0].Name, artist) {
			continue
		}
		var page struct {
			ID          string `json:"id"`
			Popularity  int    `json:"popularity"`
			ExternalIDs struct {
				UPC string `json:"upc"`
			} `json:"external_ids"`
			Images []struct {
				URL   string `json:"url"`
				Width int    `json:"width"`
			} `json:"images"`
		}
		if err := c.get(ctx, apiURL+"/albums/"+url.PathEscape(hit.ID), &page); err != nil {
			return Album{}, wrap("album "+hit.ID, err)
		}
		album := Album{ID: page.ID, Popularity: page.Popularity, UPC: page.ExternalIDs.UPC}
		widest := 0
		for _, image := range page.Images {
			if image.Width > widest {
				widest, album.Cover = image.Width, image.URL
			}
		}
		return album, nil
	}
	return Album{}, ErrNotFound
}

func wrap(what string, err error) error {
	if errors.Is(err, ErrNoKey) {
		return err
	}
	return fmt.Errorf("spotify: %s: %w", what, err)
}
