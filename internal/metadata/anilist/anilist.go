// Package anilist reads AniList's GraphQL API, which needs no key: the romaji, English and native titles and the
// synonyms release groups name anime by, plus banner and cover art. It supplies aliases and artwork, never numbering.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means AniList knows no anime under that id.
var ErrNotFound = errors.New("anilist: not found")

const (
	endpoint         = "https://graphql.anilist.co"
	maxResponseBytes = 4 << 20
	query            = `query($id:Int){Media(id:$id,type:ANIME){id title{romaji english native} synonyms format episodes status seasonYear bannerImage coverImage{extraLarge} genres idMal}}`
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks AniList.
type Client struct {
	http Doer
	url  string
}

// New builds a client over an httpx client carrying AniList's own rate bucket.
func New(client Doer) Client { return Client{http: client, url: endpoint} }

// Media is one anime entry; it is one season or cour, as AniList splits them.
type Media struct {
	ID    int64 `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	Synonyms    []string `json:"synonyms"`
	Format      string   `json:"format"`
	Episodes    int      `json:"episodes"`
	Status      string   `json:"status"`
	SeasonYear  int      `json:"seasonYear"`
	BannerImage string   `json:"bannerImage"`
	CoverImage  struct {
		ExtraLarge string `json:"extraLarge"`
	} `json:"coverImage"`
	Genres []string `json:"genres"`
	IDMal  int64    `json:"idMal"`
}

// Titles lists every name the entry answers to, the three titles first, without repeats.
func (m Media) Titles() []string {
	var names []string
	for _, name := range append([]string{m.Title.Romaji, m.Title.English, m.Title.Native}, m.Synonyms...) {
		if name == "" {
			continue
		}
		duplicate := false
		for _, have := range names {
			duplicate = duplicate || have == name
		}
		if !duplicate {
			names = append(names, name)
		}
	}
	return names
}

// Media reads one anime by AniList id.
func (c Client) Media(ctx context.Context, id int64) (Media, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"id": id}})
	if err != nil {
		return Media{}, fmt.Errorf("anilist: %d: %w", id, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Media{}, fmt.Errorf("anilist: %d: %w", id, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return Media{}, ErrNotFound
		}
		return Media{}, fmt.Errorf("anilist: %d: %w", id, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return Media{}, fmt.Errorf("anilist: %d: read: %w", id, err)
	}
	var answer struct {
		Data struct {
			Media *Media `json:"Media"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Media{}, fmt.Errorf("anilist: %d: decode: %w", id, err)
	}
	if answer.Data.Media == nil {
		return Media{}, ErrNotFound
	}
	return *answer.Data.Media, nil
}

// Ping asks for a well-known entry, for the connection test.
func (c Client) Ping(ctx context.Context) error {
	_, err := c.Media(ctx, 1)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
