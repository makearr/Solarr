// Package theporndb reads ThePornDB's REST API (api.theporndb.net), which holds the films its GraphQL endpoint and the
// stash-box servers do not. It takes the same API key as ThePornDB's GraphQL.
package theporndb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNoKey means no key is stored; ErrNotFound that the server knows no film under that id.
var (
	ErrNoKey    = errors.New("theporndb: no API key")
	ErrNotFound = errors.New("theporndb: not found")
)

const (
	baseURL          = "https://api.theporndb.net"
	maxResponseBytes = 8 << 20
	// SearchPageSize is the most one search keeps.
	SearchPageSize = 10
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks the REST API with the owner's key.
type Client struct {
	http Doer
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none.
func New(client Doer, key func() string) Client { return Client{http: client, key: key, base: baseURL} }

// Configured says a key is stored.
func (c Client) Configured() bool { return c.key != nil && strings.TrimSpace(c.key()) != "" }

// Performer is a performer credited on a film: the canonical one the site's own entry points to.
type Performer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	Extra  Extra  `json:"extra"`
	Parent *struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Image string `json:"image"`
		Extra Extra  `json:"extra"`
	} `json:"parent"`
}

// Extra is the part of a performer's facts the credit needs.
type Extra struct {
	Gender string `json:"gender"`
}

// Canonical is the performer's own id, name and picture: the parent entry when the site's is one of its aliases.
func (p Performer) Canonical() (id, name, image, gender string) {
	id, name, image, gender = p.ID, p.Name, p.Image, p.Extra.Gender
	if p.Parent != nil && p.Parent.ID != "" {
		id, name = p.Parent.ID, p.Parent.Name
		if p.Parent.Image != "" {
			image = p.Parent.Image
		}
		if p.Parent.Extra.Gender != "" {
			gender = p.Parent.Extra.Gender
		}
	}
	return id, name, image, gender
}

// Site is the studio a film is from.
type Site struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Logo    string `json:"logo"`
	Network *struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"network"`
}

// Tag is one of the server's descriptive tags.
type Tag struct {
	Name string `json:"name"`
}

// Director is a film's director.
type Director struct {
	Name string `json:"name"`
}

// Movie is one film; Duration is in seconds.
type Movie struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Date        string      `json:"date"`
	Duration    int         `json:"duration"`
	URL         string      `json:"url"`
	Image       string      `json:"image"`
	BackImage   string      `json:"back_image"`
	Poster      string      `json:"poster"`
	Performers  []Performer `json:"performers"`
	Site        *Site       `json:"site"`
	Tags        []Tag       `json:"tags"`
	Directors   []Director  `json:"directors"`
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	if !c.Configured() {
		return ErrNoKey
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.key()))
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	var refused *httpx.StatusError
	if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// SearchMovies finds films by words of their title.
func (c Client) SearchMovies(ctx context.Context, term string, limit int) ([]Movie, error) {
	var found struct {
		Data []Movie `json:"data"`
	}
	err := c.get(ctx, "/movies", url.Values{"q": {strings.TrimSpace(term)}, "per_page": {fmt.Sprint(min(max(limit, 1), SearchPageSize))}}, &found)
	return found.Data, err
}

// Movie reads one film by id.
func (c Client) Movie(ctx context.Context, id string) (Movie, error) {
	var found struct {
		Data Movie `json:"data"`
	}
	if err := c.get(ctx, "/movies/"+url.PathEscape(id), nil, &found); err != nil {
		return Movie{}, err
	}
	if found.Data.ID == "" {
		return Movie{}, ErrNotFound
	}
	return found.Data, nil
}
