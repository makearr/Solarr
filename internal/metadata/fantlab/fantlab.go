// Package fantlab reads FantLab (fantlab.ru), keyless: the Russian catalogue of fantasy and science fiction that
// Audiobookshelf offers as a provider. Titles come in Russian with the original beside them.
package fantlab

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

// ErrNotFound means FantLab knows no work under that id.
var ErrNotFound = errors.New("fantlab: not found")

const (
	baseURL          = "https://api.fantlab.ru"
	siteURL          = "https://fantlab.ru"
	maxResponseBytes = 4 << 20
	// SearchPageSize is how many works one search page lists at most.
	SearchPageSize = 20
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks FantLab.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying FantLab's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Match is one work a search found.
type Match struct {
	WorkID      int64     `json:"work_id"`
	Name        string    `json:"name"`
	RussianName string    `json:"rusname"`
	AuthorName  string    `json:"all_autor_name"`    //nolint:misspell // FantLab's own field name
	AuthorRus   string    `json:"all_autor_rusname"` //nolint:misspell // FantLab's own field name
	Year        int       `json:"year"`
	Rating      []float64 `json:"rating"`
	PictureID   int64     `json:"pic_edition_id_auto"`
}

// Cover is the work's edition picture, empty when it has none.
func (m Match) Cover() string {
	if m.PictureID <= 0 {
		return ""
	}
	return siteURL + "/images/editions/big/" + strconv.FormatInt(m.PictureID, 10)
}

// Author is one writer of a work.
type Author struct {
	Name     string `json:"name"`
	Original string `json:"name_orig"`
}

// Work is one work with what FantLab says of it.
type Work struct {
	ID          int64    `json:"work_id"`
	Name        string   `json:"work_name"`
	Original    string   `json:"work_name_orig"`
	Description string   `json:"work_description"`
	Year        int      `json:"work_year"`
	Image       string   `json:"image"`
	Authors     []Author `json:"authors"`
	Rating      struct {
		Rating string `json:"rating"`
		Voters int64  `json:"voters"`
	} `json:"rating"`
}

// Cover is the work's picture as an address, empty when it has none.
func (w Work) Cover() string {
	if w.Image == "" {
		return ""
	}
	if parsed, err := url.Parse(w.Image); err == nil {
		parsed.RawQuery = ""
		return siteURL + parsed.Path
	}
	return ""
}

// Search finds works by title; FantLab lists the best matches first.
func (c Client) Search(ctx context.Context, title string, limit int) ([]Match, error) {
	var found struct {
		Matches []Match `json:"matches"`
	}
	if err := c.http.GetJSON(ctx, c.base+"/search-works?q="+url.QueryEscape(title)+"&page=1", maxResponseBytes, &found); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > SearchPageSize {
		limit = SearchPageSize
	}
	if len(found.Matches) > limit {
		found.Matches = found.Matches[:limit]
	}
	return found.Matches, nil
}

// Work reads one work by its id.
func (c Client) Work(ctx context.Context, id int64) (Work, error) {
	var work Work
	if err := c.http.GetJSON(ctx, c.base+"/work/"+strconv.FormatInt(id, 10)+"/extended", maxResponseBytes, &work); err != nil {
		return Work{}, err
	}
	if work.ID == 0 {
		return Work{}, ErrNotFound
	}
	return work, nil
}
