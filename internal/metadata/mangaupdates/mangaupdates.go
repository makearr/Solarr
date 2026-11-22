// Package mangaupdates reads MangaUpdates (mangaupdates.com): manga titles and associated names, type, year, status, publishers, genres and authors.
// Reads are public and need no key; its terms ask for a credit and reasonable spacing. Response shapes are from its OpenAPI as remembered on 2026-11-13: the API did not answer on 2026-11-22.
package mangaupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means MangaUpdates knows no series under the id.
var ErrNotFound = errors.New("mangaupdates: not found")

const (
	baseURL          = "https://api.mangaupdates.com/v1"
	maxResponseBytes = 2 << 20
	// SearchPageSize is how many series one search page lists.
	SearchPageSize = 25
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks MangaUpdates.
type Client struct {
	http Doer
	base string
}

// New builds a client over an httpx client carrying MangaUpdates' own rate bucket.
func New(client Doer) Client { return Client{http: client, base: baseURL} }

// Series is one MangaUpdates series.
type Series struct {
	ID          int64
	Title       string
	Associated  []string
	Description string
	CoverURL    string
	// Type is MangaUpdates' word: Manga, Manhwa, Manhua, Novel, Doujinshi, OEL, Artbook.
	Type string
	Year int
	// Status is the free text of the series' volume count and whether it is complete ("12 Volumes (Complete)").
	Status     string
	Licensed   bool
	Completed  bool
	Genres     []string
	Authors    []Author
	Publishers []Publisher
	// Volumes is the volume count read from Status, 0 when it says none.
	Volumes int
}

// Author is one credited person; Type is "Author" or "Artist".
type Author struct{ Name, Type string }

// Publisher is one publisher; Type is "Original" or "English".
type Publisher struct{ Name, Type string }

type seriesJSON struct {
	ID         int64  `json:"series_id"`
	Title      string `json:"title"`
	Associated []struct {
		Title string `json:"title"`
	} `json:"associated"`
	Description string `json:"description"`
	Image       struct {
		URL struct {
			Original string `json:"original"`
		} `json:"url"`
	} `json:"image"`
	Type      string `json:"type"`
	Year      string `json:"year"`
	Status    string `json:"status"`
	Licensed  bool   `json:"licensed"`
	Completed bool   `json:"completed"`
	Genres    []struct {
		Genre string `json:"genre"`
	} `json:"genres"`
	Authors []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"authors"`
	Publishers []struct {
		Name string `json:"publisher_name"`
		Type string `json:"type"`
	} `json:"publishers"`
}

func (s seriesJSON) series() Series {
	out := Series{ID: s.ID, Title: s.Title, Description: s.Description, CoverURL: s.Image.URL.Original, Type: s.Type, Status: s.Status, Licensed: s.Licensed, Completed: s.Completed}
	out.Year, _ = strconv.Atoi(strings.TrimSpace(s.Year))
	for _, a := range s.Associated {
		out.Associated = append(out.Associated, a.Title)
	}
	for _, g := range s.Genres {
		out.Genres = append(out.Genres, g.Genre)
	}
	for _, a := range s.Authors {
		out.Authors = append(out.Authors, Author{Name: a.Name, Type: a.Type})
	}
	for _, p := range s.Publishers {
		out.Publishers = append(out.Publishers, Publisher{Name: p.Name, Type: p.Type})
	}
	out.Volumes = volumesIn(s.Status)
	return out
}

// volumesIn reads "12 Volumes (Complete)" as 12; "Ongoing" and an empty text are 0.
func volumesIn(status string) int {
	fields := strings.Fields(status)
	for i := 1; i < len(fields); i++ {
		if strings.HasPrefix(strings.ToLower(fields[i]), "volume") {
			n, _ := strconv.Atoi(fields[i-1])
			return n
		}
	}
	return 0
}

// IDFromCode turns the short code of a series' address ("8jqx4pm") into the numeric id the API takes: the code is the id in base 36.
func IDFromCode(code string) (int64, bool) {
	id, err := strconv.ParseInt(strings.ToLower(strings.TrimSpace(code)), 36, 64)
	return id, err == nil && id > 0
}

func (c Client) send(ctx context.Context, method, path string, body []byte, into any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mangaupdates: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("mangaupdates: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(into); err != nil {
		return fmt.Errorf("mangaupdates: decode: %w", err)
	}
	return nil
}

// Series reads one series by numeric id.
func (c Client) Series(ctx context.Context, id int64) (Series, error) {
	var found seriesJSON
	if err := c.send(ctx, http.MethodGet, "/series/"+strconv.FormatInt(id, 10), nil, &found); err != nil {
		return Series{}, err
	}
	if found.ID == 0 {
		return Series{}, ErrNotFound
	}
	return found.series(), nil
}

// SearchSeries looks series up by title, SearchPageSize at a time from page 1; more is true while another page follows.
func (c Client) SearchSeries(ctx context.Context, title string, page int) (found []Series, more bool, err error) {
	body, err := json.Marshal(map[string]any{"search": title, "page": max(page, 1), "perpage": SearchPageSize})
	if err != nil {
		return nil, false, err
	}
	var answer struct {
		TotalHits int `json:"total_hits"`
		Results   []struct {
			Record seriesJSON `json:"record"`
		} `json:"results"`
	}
	if err := c.send(ctx, http.MethodPost, "/series/search", body, &answer); err != nil {
		return nil, false, err
	}
	for _, r := range answer.Results {
		found = append(found, r.Record.series())
	}
	return found, max(page, 1)*SearchPageSize < answer.TotalHits, nil
}
