// Package tvdb reads TheTVDB's own v4 API: series with artwork, networks and content ratings, and every ordering a
// series has (aired, DVD, absolute, alternate). Skyhook is the keyless route to the same data; this is the direct one.
package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means TheTVDB knows nothing under that id.
var ErrNotFound = errors.New("tvdb: not found")

// ErrBadKey means TheTVDB refused the project key.
var ErrBadKey = errors.New("tvdb: the key was refused")

// ProjectKey is Solarr's own TheTVDB v4 project key, used until the user stores one of their own.
const ProjectKey = "07201bbf-755b-49bb-b6c6-f78c75ae65c3"

const (
	baseURL          = "https://api4.thetvdb.com/v4"
	maxResponseBytes = 32 << 20
	// tokenLife is shorter than the month a token is valid for, so a long-running process renews before refusal.
	tokenLife = 7 * 24 * time.Hour
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks TheTVDB. Key is read on every login so a key saved in the interface applies at once; the bearer token
// it earns is kept until it ages out or is refused.
type Client struct {
	http Doer
	key  func() string
	pin  func() string
	base string
	now  func() time.Time
	auth *session
}

type session struct {
	mu      sync.Mutex
	token   string
	forKey  string
	expires time.Time
}

// New builds a client over an httpx client carrying TheTVDB's own rate bucket. pin may be nil.
func New(client Doer, key, pin func() string, now func() time.Time) Client {
	return Client{http: client, key: key, pin: pin, base: baseURL, now: now, auth: &session{}}
}

// Names is a translation set; TheTVDB spells languages with three letters.
type Names struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

// Named is an id and a name (genres, companies, statuses).
type Named struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Company is a network or studio.
type Company struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
}

// Rating is one country's certification.
type Rating struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// RemoteID is the series' id at another site; Type 2 is IMDb, 12 is TheMovieDB, 4 the official site, 13 EIDR.
type RemoteID struct {
	ID         string `json:"id"`
	Type       int    `json:"type"`
	SourceName string `json:"sourceName"`
}

// SeasonType names one ordering a series has.
type SeasonType struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Type is "official", "dvd", "absolute", "alternate", "regional" or "altdvd".
	Type string `json:"type"`
}

// Season is one season in one ordering.
type Season struct {
	ID     int64      `json:"id"`
	Number int        `json:"number"`
	Name   string     `json:"name"`
	Image  string     `json:"image"`
	Type   SeasonType `json:"type"`
}

// Character is a cast entry; PeopleType is "Actor", "Director", "Writer" and so on.
type Character struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PeopleID   int64  `json:"peopleId"`
	PersonName string `json:"personName"`
	PeopleType string `json:"peopleType"`
	Image      string `json:"image"`
	PersonImg  string `json:"personImgURL"`
	Sort       int    `json:"sort"`
	IsFeatured bool   `json:"isFeatured"`
}

// Artwork is one picture; Type is TheTVDB's numeric kind (2 poster, 3 background, 1 banner, 23 clearlogo).
type Artwork struct {
	Image    string  `json:"image"`
	Type     int     `json:"type"`
	Language string  `json:"language"`
	Score    float64 `json:"score"`
}

// Series is one show as the extended answer carries it.
type Series struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	Slug             string       `json:"slug"`
	Image            string       `json:"image"`
	Overview         string       `json:"overview"`
	FirstAired       string       `json:"firstAired"`
	LastAired        string       `json:"lastAired"`
	Year             string       `json:"year"`
	AverageRuntime   int          `json:"averageRuntime"`
	OriginalCountry  string       `json:"originalCountry"`
	OriginalLanguage string       `json:"originalLanguage"`
	Score            int64        `json:"score"`
	Status           Named        `json:"status"`
	Aliases          []Names      `json:"aliases"`
	Genres           []Named      `json:"genres"`
	Companies        []Company    `json:"companies"`
	OriginalNetwork  Company      `json:"originalNetwork"`
	LatestNetwork    Company      `json:"latestNetwork"`
	RemoteIDs        []RemoteID   `json:"remoteIds"`
	ContentRatings   []Rating     `json:"contentRatings"`
	Seasons          []Season     `json:"seasons"`
	SeasonTypes      []SeasonType `json:"seasonTypes"`
	Characters       []Character  `json:"characters"`
	Artworks         []Artwork    `json:"artworks"`
	AirsTime         string       `json:"airsTime"`
}

// RemoteID answers the id the series has at a named site ("IMDB", "TheMovieDB.com"), empty when none.
func (s Series) RemoteID(sourceName string) string {
	for _, remote := range s.RemoteIDs {
		if strings.EqualFold(remote.SourceName, sourceName) {
			return remote.ID
		}
	}
	return ""
}

// Series reads one show with its characters, artwork, seasons and ratings.
func (c Client) Series(ctx context.Context, id int64) (Series, error) {
	var series Series
	return series, c.get(ctx, "/series/"+strconv.FormatInt(id, 10)+"/extended", url.Values{"short": {"false"}}, &series)
}

// Episode is one episode in one ordering.
type Episode struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	Aired        string `json:"aired"`
	Runtime      int    `json:"runtime"`
	Image        string `json:"image"`
	Number       int    `json:"number"`
	SeasonNumber int    `json:"seasonNumber"`
	// AbsoluteNumber is 0 when the ordering has none.
	AbsoluteNumber int `json:"absoluteNumber"`
	// FinaleType is "series", "season", "midseason" or empty.
	FinaleType string `json:"finaleType"`
	IsMovie    int    `json:"isMovie"`
}

// Episodes reads every episode of a series in one ordering ("default", "official", "dvd", "absolute", "alternate",
// "regional", "altdvd"), following the pages.
func (c Client) Episodes(ctx context.Context, id int64, order string) ([]Episode, error) {
	var all []Episode
	for page := 0; page < 200; page++ {
		var answer struct {
			Episodes []Episode `json:"episodes"`
		}
		links, err := c.getPaged(ctx, "/series/"+strconv.FormatInt(id, 10)+"/episodes/"+url.PathEscape(order),
			url.Values{"page": {strconv.Itoa(page)}}, &answer)
		if err != nil {
			return nil, err
		}
		all = append(all, answer.Episodes...)
		if links.Next == nil {
			return all, nil
		}
	}
	return all, nil
}

// Hit is one search result.
type Hit struct {
	TVDBID    string     `json:"tvdb_id"`
	Name      string     `json:"name"`
	Year      string     `json:"year"`
	Overview  string     `json:"overview"`
	ImageURL  string     `json:"image_url"`
	Network   string     `json:"network"`
	Status    string     `json:"status"`
	Country   string     `json:"country"`
	Primary   string     `json:"primary_language"`
	Slug      string     `json:"slug"`
	RemoteIDs []RemoteID `json:"remote_ids"`
}

// Search finds series by name.
func (c Client) Search(ctx context.Context, term string) ([]Hit, error) {
	var hits []Hit
	return hits, c.get(ctx, "/search", url.Values{"query": {term}, "type": {"series"}}, &hits)
}

// Ping logs in, which is the whole of what a connection test needs to know.
func (c Client) Ping(ctx context.Context) error {
	_, err := c.login(ctx, true)
	return err
}

type links struct {
	Next *string `json:"next"`
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	_, err := c.getPaged(ctx, path, query, into)
	return err
}

func (c Client) getPaged(ctx context.Context, path string, query url.Values, into any) (links, error) {
	token, err := c.login(ctx, false)
	if err != nil {
		return links{}, err
	}
	reply, status, err := c.send(ctx, http.MethodGet, path, query, nil, token)
	if status == http.StatusUnauthorized {
		if token, err = c.login(ctx, true); err != nil {
			return links{}, err
		}
		reply, status, err = c.send(ctx, http.MethodGet, path, query, nil, token)
	}
	if err != nil {
		return links{}, err
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Links links           `json:"links"`
	}
	if err := json.Unmarshal(reply, &envelope); err != nil {
		return links{}, fmt.Errorf("tvdb: %s: decode: %w", path, err)
	}
	if status == http.StatusNotFound {
		return links{}, ErrNotFound
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		return links{}, fmt.Errorf("tvdb: %s: decode: %w", path, err)
	}
	return envelope.Links, nil
}

func (c Client) login(ctx context.Context, force bool) (string, error) {
	key := ""
	if c.key != nil {
		key = strings.TrimSpace(c.key())
	}
	if key == "" {
		return "", ErrBadKey
	}
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	if !force && c.auth.token != "" && c.auth.forKey == key && c.now().Before(c.auth.expires) {
		return c.auth.token, nil
	}
	body := map[string]string{"apikey": key}
	if c.pin != nil {
		if pin := strings.TrimSpace(c.pin()); pin != "" {
			body["pin"] = pin
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("tvdb: login: %w", err)
	}
	reply, status, err := c.send(ctx, http.MethodPost, "/login", nil, encoded, "")
	if status == http.StatusUnauthorized {
		return "", ErrBadKey
	}
	if err != nil {
		return "", err
	}
	var answer struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(reply, &answer); err != nil || answer.Data.Token == "" {
		return "", fmt.Errorf("tvdb: login: no token in the answer")
	}
	c.auth.token, c.auth.forKey, c.auth.expires = answer.Data.Token, key, c.now().Add(tokenLife)
	return c.auth.token, nil
}

// send answers the body and the status; a 401 and a 404 are statuses, not errors, so the callers can react.
func (c Client) send(ctx context.Context, method, path string, query url.Values, body []byte, token string) ([]byte, int, error) {
	address := c.base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("tvdb: %s: %w", path, err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) && (refused.Status == http.StatusUnauthorized || refused.Status == http.StatusNotFound) {
			return []byte(`{}`), refused.Status, nil
		}
		return nil, 0, fmt.Errorf("tvdb: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	reply, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("tvdb: %s: read: %w", path, err)
	}
	return reply, response.StatusCode, nil
}
