// Package whisparr reads Whisparr's own metadata server (api.whisparr.com/v3), which needs no key and is backed by
// ThePornDB: scenes with their credits, and sites with their scenes. It is someone else's service, so it is asked
// politely and never relied on.
package whisparr

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

// ErrNotFound means the server knows no site under that id.
var ErrNotFound = errors.New("whisparr: not found")

const (
	baseURL          = "https://api.whisparr.com/v3"
	maxResponseBytes = 16 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks the server.
type Client struct {
	http Doer
	base string
}

// New builds a client over an httpx client carrying the server's own rate bucket.
func New(client Doer) Client { return Client{http: client, base: baseURL} }

// Image is one picture of a kind: Poster, Logo, Headshot.
type Image struct {
	CoverType string `json:"CoverType"`
	URL       string `json:"Url"`
}

// Credit is one performer of a scene; ForeignGuid is their ThePornDB id.
type Credit struct {
	Name        string  `json:"Name"`
	Gender      string  `json:"Gender"`
	Character   string  `json:"Character"`
	ForeignGUID string  `json:"ForeignGuid"`
	Images      []Image `json:"Images"`
}

// Scene is one scene; ForeignGuid is its ThePornDB id and SiteID the site's ForeignId.
type Scene struct {
	Title       string   `json:"Title"`
	Slug        string   `json:"Slug"`
	Overview    string   `json:"Overview"`
	ReleaseDate string   `json:"ReleaseDate"`
	Duration    int      `json:"Duration"`
	SiteID      int      `json:"SiteId"`
	Type        string   `json:"Type"`
	Genres      []string `json:"Genres"`
	Credits     []Credit `json:"Credits"`
	Images      []Image  `json:"Images"`
	ForeignGUID string   `json:"ForeignGuid"`
	ForeignID   int      `json:"ForeignId"`
}

// Site is a studio or site, with its scenes when read by id.
type Site struct {
	Title     string  `json:"Title"`
	Slug      string  `json:"Slug"`
	Overview  string  `json:"Overview"`
	Network   string  `json:"Network"`
	Homepage  string  `json:"Homepage"`
	Images    []Image `json:"Images"`
	Scenes    []Scene `json:"Episodes"`
	ForeignID int     `json:"ForeignId"`
}

func (c Client) get(ctx context.Context, route string, query url.Values, into any) error {
	target := c.base + "/" + route
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("whisparr: status %d", response.StatusCode)
	}
	raw, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// SearchScenes finds scenes by words of their title; the server answers a bad query with 400, which is no scenes.
func (c Client) SearchScenes(ctx context.Context, term string) ([]Scene, error) {
	var scenes []Scene
	err := c.get(ctx, "scene/search", url.Values{"q": {term}}, &scenes)
	return scenes, err
}

// SearchSites finds sites and studios by name.
func (c Client) SearchSites(ctx context.Context, term string) ([]Site, error) {
	var sites []Site
	err := c.get(ctx, "site/search", url.Values{"q": {term}}, &sites)
	return sites, err
}

// FindSite reads one site, with its scenes, by its ForeignId. The server answers an unknown id with an empty site.
func (c Client) FindSite(ctx context.Context, id int) (Site, error) {
	var site Site
	if err := c.get(ctx, "site/"+strconv.Itoa(id), nil, &site); err != nil {
		return Site{}, err
	}
	if site.Title == "" {
		return Site{}, ErrNotFound
	}
	return site, nil
}
