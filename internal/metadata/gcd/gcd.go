// Package gcd reads the Grand Comics Database (comics.org): deep bibliographic data for comic series and issues.
// Its REST API answers without an account (checked 2026-11-22); the data is CC BY-SA 4.0, so a library that uses it must credit comics.org.
package gcd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotFound means GCD knows nothing under the id.
var ErrNotFound = errors.New("gcd: not found")

const (
	baseURL          = "https://www.comics.org/api/"
	maxResponseBytes = 4 << 20
	// PageSize is how many series one search page lists.
	PageSize = 50
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks GCD; it needs no key.
type Client struct {
	http Getter
	base string
}

// New builds a client.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Series is one GCD series.
type Series struct {
	ID        int
	Name      string
	Country   string
	Language  string
	YearBegan int
	YearEnded int
	// IssueIDs are the series' active issues in GCD's order.
	IssueIDs []int
	// PublisherID is the publisher's id, 0 when none is given.
	PublisherID int
	// Format is GCD's "publishing format" ("was ongoing series", "was limited series"...).
	Format string
}

// Issue is one GCD issue.
type Issue struct {
	ID          int
	SeriesID    int
	SeriesName  string
	Number      string
	Title       string
	KeyDate     string
	OnSaleDate  string
	Pages       float64
	Price       string
	ISBN        string
	Barcode     string
	Rating      string
	Notes       string
	VariantOfID int
}

// Publisher is a GCD publisher.
type Publisher struct {
	ID      int
	Name    string
	Country string
}

type seriesJSON struct {
	APIURL      string   `json:"api_url"`
	Name        string   `json:"name"`
	Country     string   `json:"country"`
	Language    string   `json:"language"`
	Active      []string `json:"active_issues"`
	Format      string   `json:"publishing_format"`
	YearBegan   int      `json:"year_began"`
	YearEnded   int      `json:"year_ended"`
	PublisherAt string   `json:"publisher"`
}

type pageJSON struct {
	Count   int          `json:"count"`
	Next    *string      `json:"next"`
	Results []seriesJSON `json:"results"`
}

type issueJSON struct {
	APIURL     string  `json:"api_url"`
	SeriesName string  `json:"series_name"`
	Number     string  `json:"number"`
	Title      string  `json:"title"`
	KeyDate    string  `json:"key_date"`
	OnSaleDate string  `json:"on_sale_date"`
	PageCount  string  `json:"page_count"`
	Price      string  `json:"price"`
	ISBN       string  `json:"isbn"`
	Barcode    string  `json:"barcode"`
	Rating     string  `json:"rating"`
	Notes      string  `json:"notes"`
	VariantOf  *string `json:"variant_of"`
	SeriesAt   string  `json:"series"`
}

type publisherJSON struct {
	APIURL  string `json:"api_url"`
	Name    string `json:"name"`
	Country string `json:"country"`
}

// idOf takes the numeric id out of an address like ".../api/series/10814/?format=json".
func idOf(address string) int {
	path := address
	if u, err := url.Parse(address); err == nil {
		path = u.Path
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return 0
	}
	id, _ := strconv.Atoi(parts[len(parts)-1])
	return id
}

func (s seriesJSON) series() Series {
	out := Series{ID: idOf(s.APIURL), Name: s.Name, Country: s.Country, Language: s.Language, YearBegan: s.YearBegan, YearEnded: s.YearEnded, Format: s.Format, PublisherID: idOf(s.PublisherAt)}
	for _, a := range s.Active {
		out.IssueIDs = append(out.IssueIDs, idOf(a))
	}
	return out
}

// SearchSeries looks series up by name, one page at a time from page 1; more is true while another page follows.
func (c Client) SearchSeries(ctx context.Context, name string, page int) (series []Series, more bool, err error) {
	var found pageJSON
	address := c.base + "series/name/" + url.PathEscape(name) + "/?format=json&page=" + strconv.Itoa(max(page, 1))
	if err := c.http.GetJSON(ctx, address, maxResponseBytes, &found); err != nil {
		return nil, false, fmt.Errorf("gcd: %w", err)
	}
	for _, s := range found.Results {
		series = append(series, s.series())
	}
	return series, found.Next != nil, nil
}

// Series reads one series with the ids of its issues.
func (c Client) Series(ctx context.Context, id int) (Series, error) {
	var found seriesJSON
	if err := c.http.GetJSON(ctx, c.base+"series/"+strconv.Itoa(id)+"/?format=json", maxResponseBytes, &found); err != nil {
		return Series{}, fmt.Errorf("gcd: %w", err)
	}
	if found.APIURL == "" {
		return Series{}, ErrNotFound
	}
	return found.series(), nil
}

// Issue reads one issue.
func (c Client) Issue(ctx context.Context, id int) (Issue, error) {
	var found issueJSON
	if err := c.http.GetJSON(ctx, c.base+"issue/"+strconv.Itoa(id)+"/?format=json", maxResponseBytes, &found); err != nil {
		return Issue{}, fmt.Errorf("gcd: %w", err)
	}
	if found.APIURL == "" {
		return Issue{}, ErrNotFound
	}
	out := Issue{ID: idOf(found.APIURL), SeriesID: idOf(found.SeriesAt), SeriesName: found.SeriesName, Number: strings.TrimSpace(found.Number), Title: found.Title,
		KeyDate: found.KeyDate, OnSaleDate: found.OnSaleDate, Price: found.Price, ISBN: found.ISBN, Barcode: found.Barcode, Rating: found.Rating, Notes: found.Notes}
	out.Pages, _ = strconv.ParseFloat(found.PageCount, 64)
	if found.VariantOf != nil {
		out.VariantOfID = idOf(*found.VariantOf)
	}
	return out, nil
}

// Publisher reads one publisher.
func (c Client) Publisher(ctx context.Context, id int) (Publisher, error) {
	var found publisherJSON
	if err := c.http.GetJSON(ctx, c.base+"publisher/"+strconv.Itoa(id)+"/?format=json", maxResponseBytes, &found); err != nil {
		return Publisher{}, fmt.Errorf("gcd: %w", err)
	}
	if found.APIURL == "" {
		return Publisher{}, ErrNotFound
	}
	return Publisher{ID: idOf(found.APIURL), Name: found.Name, Country: found.Country}, nil
}
