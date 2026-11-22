// Package comicvine reads ComicVine (comicvine.gamespot.com): comic series ("volumes"), their issues, covers, credits and story arcs.
// It needs the owner's free key; ComicVine's terms are non-commercial and allow 200 requests per resource an hour, so answers are cached by the caller.
package comicvine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrNotFound means ComicVine knows nothing under the id.
var ErrNotFound = errors.New("comicvine: not found")

// ErrNoKey means no key is stored, without which ComicVine answers nothing.
var ErrNoKey = errors.New("comicvine: no API key")

const (
	baseURL          = "https://comicvine.gamespot.com/api/"
	maxResponseBytes = 4 << 20
	// PageSize is how many issues one request lists, ComicVine's own maximum.
	PageSize = 100
)

// Resource prefixes ComicVine puts before an id in an address.
const (
	seriesPrefix = "4050-"
	issuePrefix  = "4000-"
)

// Status codes ComicVine reports inside a 200 answer.
const (
	statusOK       = 1
	statusNotFound = 101
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks ComicVine with the owner's key.
type Client struct {
	http Getter
	key  func() string
	base string
}

// New builds a client; key answers the owner's key, empty while there is none.
func New(client Getter, key func() string) Client {
	return Client{http: client, key: key, base: baseURL}
}

// Configured says a key is stored.
func (c Client) Configured() bool { return c.key != nil && c.key() != "" }

// Series is a ComicVine volume: one run of a title.
type Series struct {
	ID          int
	Name        string
	StartYear   int
	Publisher   string
	IssueCount  int
	Description string
	CoverURL    string
}

// Issue is one issue of a series.
type Issue struct {
	ID          int
	SeriesID    int
	Number      string
	Title       string
	CoverDate   string
	StoreDate   string
	Description string
	CoverURL    string
	Credits     []Credit
	Arcs        []Arc
}

// Credit is one person's role on an issue, in ComicVine's words ("writer, penciler").
type Credit struct {
	ID    int
	Name  string
	Roles []string
}

// Arc is a story arc an issue belongs to.
type Arc struct {
	ID   int
	Name string
}

type imageJSON struct {
	Original string `json:"original_url"`
	Medium   string `json:"medium_url"`
}

// envelope holds the results raw, because ComicVine answers an empty list where a single object was asked for and failed.
type envelope struct {
	Error      string          `json:"error"`
	StatusCode int             `json:"status_code"`
	Results    json.RawMessage `json:"results"`
}

type seriesJSON struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	StartYear   string     `json:"start_year"`
	Publisher   *namedJSON `json:"publisher"`
	IssueCount  int        `json:"count_of_issues"`
	Description string     `json:"description"`
	Image       *imageJSON `json:"image"`
}

type namedJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type issueJSON struct {
	ID          int        `json:"id"`
	Number      string     `json:"issue_number"`
	Name        string     `json:"name"`
	CoverDate   string     `json:"cover_date"`
	StoreDate   string     `json:"store_date"`
	Description string     `json:"description"`
	Image       *imageJSON `json:"image"`
	Volume      *namedJSON `json:"volume"`
	People      []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"person_credits"`
	Arcs []namedJSON `json:"story_arc_credits"`
}

const (
	seriesFields = "id,name,start_year,publisher,count_of_issues,description,image"
	issueFields  = "id,issue_number,name,cover_date,store_date,description,image,volume"
)

func (s seriesJSON) series() Series {
	out := Series{ID: s.ID, Name: s.Name, IssueCount: s.IssueCount, Description: s.Description}
	out.StartYear, _ = strconv.Atoi(strings.TrimSpace(s.StartYear))
	if s.Publisher != nil {
		out.Publisher = s.Publisher.Name
	}
	if s.Image != nil {
		out.CoverURL = firstNonEmpty(s.Image.Original, s.Image.Medium)
	}
	return out
}

func (i issueJSON) issue() Issue {
	out := Issue{ID: i.ID, Number: strings.TrimSpace(i.Number), Title: i.Name, CoverDate: i.CoverDate, StoreDate: i.StoreDate, Description: i.Description}
	if i.Volume != nil {
		out.SeriesID = i.Volume.ID
	}
	if i.Image != nil {
		out.CoverURL = firstNonEmpty(i.Image.Original, i.Image.Medium)
	}
	byPerson := map[int]int{}
	for _, p := range i.People {
		var roles []string
		for _, r := range strings.Split(p.Role, ",") {
			if r = strings.TrimSpace(r); r != "" {
				roles = append(roles, r)
			}
		}
		if at, ok := byPerson[p.ID]; ok {
			out.Credits[at].Roles = append(out.Credits[at].Roles, roles...)
			continue
		}
		byPerson[p.ID] = len(out.Credits)
		out.Credits = append(out.Credits, Credit{ID: p.ID, Name: p.Name, Roles: roles})
	}
	for _, a := range i.Arcs {
		out.Arcs = append(out.Arcs, Arc(a))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// get asks one resource and unwraps ComicVine's envelope.
func get[T any](ctx context.Context, c Client, resource string, query url.Values, into *T) error {
	if !c.Configured() {
		return ErrNoKey
	}
	query.Set("api_key", c.key())
	query.Set("format", "json")
	var answer envelope
	if err := c.http.GetJSON(ctx, c.base+resource+"?"+query.Encode(), maxResponseBytes, &answer); err != nil {
		return fmt.Errorf("comicvine: %w", err)
	}
	switch answer.StatusCode {
	case statusOK:
		if err := json.Unmarshal(answer.Results, into); err != nil {
			return fmt.Errorf("comicvine: results: %w", err)
		}
		return nil
	case statusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("comicvine: %s (status %d)", answer.Error, answer.StatusCode)
	}
}

// SearchSeries looks a series up by name; a year, when given, keeps only series that started then or a year either side.
func (c Client) SearchSeries(ctx context.Context, query string, year int) ([]Series, error) {
	var found []seriesJSON
	err := get(ctx, c, "search/", url.Values{"resources": {"volume"}, "query": {query}, "field_list": {seriesFields}, "limit": {"25"}}, &found)
	if err != nil {
		return nil, err
	}
	var out []Series
	for _, s := range found {
		series := s.series()
		if year != 0 && series.StartYear != 0 && (series.StartYear < year-1 || series.StartYear > year+1) {
			continue
		}
		out = append(out, series)
	}
	return out, nil
}

// Series reads one series.
func (c Client) Series(ctx context.Context, id int) (Series, error) {
	var found seriesJSON
	if err := get(ctx, c, "volume/"+seriesPrefix+strconv.Itoa(id)+"/", url.Values{"field_list": {seriesFields}}, &found); err != nil {
		return Series{}, err
	}
	return found.series(), nil
}

// Issues lists a series' issues, PageSize at a time from offset, oldest first; more is true while another page may follow.
func (c Client) Issues(ctx context.Context, seriesID, offset int) (issues []Issue, more bool, err error) {
	var found []issueJSON
	query := url.Values{"filter": {"volume:" + strconv.Itoa(seriesID)}, "field_list": {issueFields}, "sort": {"cover_date:asc"}, "limit": {strconv.Itoa(PageSize)}, "offset": {strconv.Itoa(offset)}}
	if err := get(ctx, c, "issues/", query, &found); err != nil {
		return nil, false, err
	}
	for _, i := range found {
		issues = append(issues, i.issue())
	}
	return issues, len(found) == PageSize, nil
}

// Issue reads one issue with its credits and story arcs.
func (c Client) Issue(ctx context.Context, id int) (Issue, error) {
	var found issueJSON
	query := url.Values{"field_list": {issueFields + ",person_credits,story_arc_credits"}}
	if err := get(ctx, c, "issue/"+issuePrefix+strconv.Itoa(id)+"/", query, &found); err != nil {
		return Issue{}, err
	}
	return found.issue(), nil
}
