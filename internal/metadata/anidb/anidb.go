// Package anidb reads AniDB's HTTP API with the owner's registered client name: an anime's rating, description and titles.
package anidb

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	baseURL          = "http://api.anidb.net:9001/httpapi"
	maxResponseBytes = 2 << 20
	// clientVersion is the version AniDB is told with the client name.
	clientVersion = "1"
)

// ErrNotFound means AniDB holds no anime under that id.
var ErrNotFound = errors.New("anidb: not found")

// ErrNoKey means no client name is stored, so nothing is asked.
var ErrNoKey = errors.New("anidb: no client name")

// ErrBanned means AniDB refused the client for asking too often; nothing more is asked until the person looks.
var ErrBanned = errors.New("anidb: client banned")

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	Get(ctx context.Context, address string) (*http.Response, error)
}

// Client asks AniDB under the client name the owner registered at anidb.net, read at each call.
type Client struct {
	http Getter
	key  func() string
}

// New builds a client.
func New(client Getter, key func() string) Client { return Client{http: client, key: key} }

// Configured says a client name is stored.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Anime is what AniDB says of one anime.
type Anime struct {
	ID          int64
	Title       string
	Description string
	StartDate   string
	// Score is the members' rating from 0 to 10 (the temporary one until the permanent one has votes), Votes how many gave one.
	Score float64
	Votes int64
}

type wireAnime struct {
	XMLName     xml.Name `xml:"anime"`
	ID          int64    `xml:"id,attr"`
	StartDate   string   `xml:"startdate"`
	Description string   `xml:"description"`
	Titles      []struct {
		Lang string `xml:"lang,attr"`
		Type string `xml:"type,attr"`
		Text string `xml:",chardata"`
	} `xml:"titles>title"`
	Permanent struct {
		Count int64   `xml:"count,attr"`
		Value float64 `xml:",chardata"`
	} `xml:"ratings>permanent"`
	Temporary struct {
		Count int64   `xml:"count,attr"`
		Value float64 `xml:",chardata"`
	} `xml:"ratings>temporary"`
}

type wireError struct {
	XMLName xml.Name `xml:"error"`
	Code    string   `xml:"code,attr"`
	Text    string   `xml:",chardata"`
}

// Anime reads one anime by its AniDB id.
func (c Client) Anime(ctx context.Context, id int64) (Anime, error) {
	client := ""
	if c.key != nil {
		client = c.key()
	}
	if client == "" {
		return Anime{}, ErrNoKey
	}
	values := url.Values{"client": {client}, "clientver": {clientVersion}, "protover": {"1"}, "request": {"anime"}, "aid": {strconv.FormatInt(id, 10)}}
	response, err := c.http.Get(ctx, baseURL+"?"+values.Encode())
	if err != nil {
		return Anime{}, fmt.Errorf("anidb: anime %d: %w", id, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return Anime{}, fmt.Errorf("anidb: read: %w", err)
	}
	return parse(data)
}

func parse(data []byte) (Anime, error) {
	var failure wireError
	if xml.Unmarshal(data, &failure) == nil && failure.XMLName.Local == "error" {
		switch {
		case strings.Contains(strings.ToLower(failure.Text), "banned"):
			return Anime{}, ErrBanned
		case strings.Contains(strings.ToLower(failure.Text), "no such anime"):
			return Anime{}, ErrNotFound
		}
		return Anime{}, fmt.Errorf("anidb: error %s: %s", failure.Code, strings.TrimSpace(failure.Text))
	}
	var wire wireAnime
	if err := xml.Unmarshal(data, &wire); err != nil {
		return Anime{}, fmt.Errorf("anidb: decode: %w", err)
	}
	anime := Anime{ID: wire.ID, Description: strings.TrimSpace(wire.Description), StartDate: wire.StartDate}
	for _, title := range wire.Titles {
		if title.Type == "main" {
			anime.Title = strings.TrimSpace(title.Text)
		}
	}
	switch {
	case wire.Permanent.Count > 0:
		anime.Score, anime.Votes = wire.Permanent.Value, wire.Permanent.Count
	case wire.Temporary.Count > 0:
		anime.Score, anime.Votes = wire.Temporary.Value, wire.Temporary.Count
	}
	return anime, nil
}
