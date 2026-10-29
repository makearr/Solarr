// Package anilist reads AniList's GraphQL API, which needs no key: the romaji, English and native titles and the
// synonyms release groups name anime by, plus banner and cover art. It supplies aliases and artwork, never numbering.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means AniList knows no anime under that id.
var ErrNotFound = errors.New("anilist: not found")

const (
	endpoint         = "https://graphql.anilist.co"
	maxResponseBytes = 4 << 20
	mediaFields      = `id title{romaji english native} synonyms format episodes status seasonYear bannerImage coverImage{extraLarge} genres idMal`
	query            = `query($id:Int){Media(id:$id,type:ANIME){` + mediaFields + `}}`
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

// post asks one GraphQL question about one entry and answers the raw reply.
func (c Client) post(ctx context.Context, question string, id int64) ([]byte, error) {
	raw, err := c.ask(ctx, question, map[string]any{"id": id})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("anilist: %d: %w", id, err)
	}
	return raw, err
}

// ask sends one GraphQL question with its variables and answers the raw reply.
func (c Client) ask(ctx context.Context, question string, variables map[string]any) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"query": question, "variables": variables})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return raw, nil
}

// Media reads one anime by AniList id.
func (c Client) Media(ctx context.Context, id int64) (Media, error) {
	raw, err := c.post(ctx, query, id)
	if err != nil {
		return Media{}, err
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

// Relation is another entry AniList says an anime is related to, and how.
type Relation struct {
	// Type is AniList's word: SEQUEL, PREQUEL, SIDE_STORY, SPIN_OFF, PARENT, ALTERNATIVE, ADAPTATION, SUMMARY, ...
	Type   string
	ID     int64
	Format string
	Title  string
}

const relationsQuery = `query($id:Int){Media(id:$id,type:ANIME){id relations{edges{relationType node{id type format title{romaji english}}}}}}`

// Related lists the anime entries this one is related to; entries of other kinds (the manga it adapts) are left out.
func (c Client) Related(ctx context.Context, id int64) ([]Relation, error) {
	raw, err := c.post(ctx, relationsQuery, id)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Data struct {
			Media *struct {
				Relations struct {
					Edges []struct {
						RelationType string `json:"relationType"`
						Node         struct {
							ID     int64  `json:"id"`
							Type   string `json:"type"`
							Format string `json:"format"`
							Title  struct {
								Romaji  string `json:"romaji"`
								English string `json:"english"`
							} `json:"title"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"relations"`
			} `json:"Media"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("anilist: %d: decode: %w", id, err)
	}
	if answer.Data.Media == nil {
		return nil, ErrNotFound
	}
	var out []Relation
	for _, edge := range answer.Data.Media.Relations.Edges {
		if edge.Node.Type != "ANIME" {
			continue
		}
		title := edge.Node.Title.English
		if title == "" {
			title = edge.Node.Title.Romaji
		}
		out = append(out, Relation{Type: edge.RelationType, ID: edge.Node.ID, Format: edge.Node.Format, Title: title})
	}
	return out, nil
}

// Ping asks for a well-known entry, for the connection test.
func (c Client) Ping(ctx context.Context) error {
	_, err := c.Media(ctx, 1)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
