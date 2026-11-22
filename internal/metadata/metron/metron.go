// Package metron reads Metron (metron.cloud): comic series with their type, issues with cover and store dates, page counts, age ratings, credits, arcs and reprints.
// It needs the owner's Metron account (HTTP basic); every shape here is from Metron's documentation as remembered on 2026-11-13 and not yet checked against an account.
package metron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means Metron knows nothing under the id.
var ErrNotFound = errors.New("metron: not found")

// ErrNoAccount means no account is stored, without which Metron answers nothing.
var ErrNoAccount = errors.New("metron: no account")

const (
	baseURL          = "https://metron.cloud/api/"
	maxResponseBytes = 4 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks Metron with the owner's account.
type Client struct {
	http    Doer
	account func() (user, password string)
	base    string
}

// New builds a client; account answers the owner's user name and password, empty while there are none.
func New(client Doer, account func() (string, string)) Client {
	return Client{http: client, account: account, base: baseURL}
}

// Configured says an account is stored.
func (c Client) Configured() bool {
	if c.account == nil {
		return false
	}
	user, password := c.account()
	return user != "" && password != ""
}

// Series is a Metron series.
type Series struct {
	ID         int
	Name       string
	Volume     int
	YearBegan  int
	YearEnded  int
	Type       string
	Publisher  string
	Imprint    string
	IssueCount int
	Status     string
	Summary    string
	// ComicVineID and GCDID bridge to the other providers; 0 when Metron has none.
	ComicVineID, GCDID int
}

// Issue is a Metron issue.
type Issue struct {
	ID        int
	SeriesID  int
	Number    string
	Titles    []string
	CoverDate string
	StoreDate string
	Pages     int
	Price     string
	Rating    string
	Summary   string
	CoverURL  string
	Credits   []Credit
	Arcs      []string
	// Reprints are the ids of the issues this issue collects; a collected edition lists the issues it holds here.
	Reprints           []int
	ComicVineID, GCDID int
}

// Credit is one person's roles on an issue.
type Credit struct {
	Name  string
	Roles []string
}

type namedJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type pageJSON[T any] struct {
	Count   int    `json:"count"`
	Next    string `json:"next"`
	Results []T    `json:"results"`
}

type seriesJSON struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Volume     int        `json:"volume"`
	YearBegan  int        `json:"year_began"`
	YearEnd    int        `json:"year_end"`
	Type       *namedJSON `json:"series_type"`
	Publisher  *namedJSON `json:"publisher"`
	Imprint    *namedJSON `json:"imprint"`
	IssueCount int        `json:"issue_count"`
	Status     string     `json:"status"`
	Desc       string     `json:"desc"`
	CVID       int        `json:"cv_id"`
	GCDID      int        `json:"gcd_id"`
}

type issueJSON struct {
	ID        int         `json:"id"`
	Series    *namedJSON  `json:"series"`
	Number    string      `json:"number"`
	Name      []string    `json:"name"`
	CoverDate string      `json:"cover_date"`
	StoreDate string      `json:"store_date"`
	Page      int         `json:"page"`
	Price     string      `json:"price"`
	Rating    *namedJSON  `json:"rating"`
	Desc      string      `json:"desc"`
	Image     string      `json:"image"`
	Arcs      []namedJSON `json:"arcs"`
	Credits   []struct {
		Creator string      `json:"creator"`
		Role    []namedJSON `json:"role"`
	} `json:"credits"`
	Reprints []struct {
		ID int `json:"id"`
	} `json:"reprints"`
	CVID  int `json:"cv_id"`
	GCDID int `json:"gcd_id"`
}

func name(n *namedJSON) string {
	if n == nil {
		return ""
	}
	return n.Name
}

func (s seriesJSON) series() Series {
	return Series{ID: s.ID, Name: s.Name, Volume: s.Volume, YearBegan: s.YearBegan, YearEnded: s.YearEnd, Type: name(s.Type), Publisher: name(s.Publisher), Imprint: name(s.Imprint),
		IssueCount: s.IssueCount, Status: s.Status, Summary: s.Desc, ComicVineID: s.CVID, GCDID: s.GCDID}
}

func (i issueJSON) issue() Issue {
	out := Issue{ID: i.ID, Number: strings.TrimSpace(i.Number), Titles: i.Name, CoverDate: i.CoverDate, StoreDate: i.StoreDate, Pages: i.Page, Price: i.Price,
		Rating: name(i.Rating), Summary: i.Desc, CoverURL: i.Image, ComicVineID: i.CVID, GCDID: i.GCDID}
	if i.Series != nil {
		out.SeriesID = i.Series.ID
	}
	for _, a := range i.Arcs {
		out.Arcs = append(out.Arcs, a.Name)
	}
	for _, c := range i.Credits {
		credit := Credit{Name: c.Creator}
		for _, r := range c.Role {
			credit.Roles = append(credit.Roles, r.Name)
		}
		out.Credits = append(out.Credits, credit)
	}
	for _, r := range i.Reprints {
		out.Reprints = append(out.Reprints, r.ID)
	}
	return out
}

func (c Client) get(ctx context.Context, resource string, query url.Values, into any) error {
	if !c.Configured() {
		return ErrNoAccount
	}
	address := c.base + resource
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("metron: %w", err)
	}
	user, password := c.account()
	request.SetBasicAuth(user, password)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return ErrNotFound
		}
		return fmt.Errorf("metron: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case response.StatusCode != http.StatusOK:
		return fmt.Errorf("metron: status %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(into); err != nil {
		return fmt.Errorf("metron: decode: %w", err)
	}
	return nil
}

// SearchSeries looks a series up by name, optionally by the year it began.
func (c Client) SearchSeries(ctx context.Context, query string, year int) ([]Series, error) {
	values := url.Values{"name": {query}}
	if year != 0 {
		values.Set("year_began", strconv.Itoa(year))
	}
	var page pageJSON[seriesJSON]
	if err := c.get(ctx, "series/", values, &page); err != nil {
		return nil, err
	}
	var out []Series
	for _, s := range page.Results {
		out = append(out, s.series())
	}
	return out, nil
}

// Series reads one series.
func (c Client) Series(ctx context.Context, id int) (Series, error) {
	var found seriesJSON
	if err := c.get(ctx, "series/"+strconv.Itoa(id)+"/", nil, &found); err != nil {
		return Series{}, err
	}
	return found.series(), nil
}

// Issues lists a series' issues one page at a time from page 1; more is true while another page follows.
func (c Client) Issues(ctx context.Context, seriesID, page int) (issues []Issue, more bool, err error) {
	var found pageJSON[issueJSON]
	if err := c.get(ctx, "issue/", url.Values{"series_id": {strconv.Itoa(seriesID)}, "page": {strconv.Itoa(max(page, 1))}}, &found); err != nil {
		return nil, false, err
	}
	for _, i := range found.Results {
		issues = append(issues, i.issue())
	}
	return issues, found.Next != "", nil
}

// Issue reads one issue with its credits and reprints.
func (c Client) Issue(ctx context.Context, id int) (Issue, error) {
	var found issueJSON
	if err := c.get(ctx, "issue/"+strconv.Itoa(id)+"/", nil, &found); err != nil {
		return Issue{}, err
	}
	return found.issue(), nil
}
