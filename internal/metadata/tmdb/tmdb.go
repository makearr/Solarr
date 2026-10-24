// Package tmdb reads The Movie Database for TV and movies: series, seasons with guest stars and episode crew, aggregate
// credits, artwork, keywords, alternative orderings and person pages. It needs the user's own key (stage 8 T4).
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotFound means TMDB knows nothing under that id.
var ErrNotFound = errors.New("tmdb: not found")

// ErrNoKey means no credential is stored, so nothing was asked.
var ErrNoKey = errors.New("tmdb: no key configured")

// ErrBadKey means TMDB refused the credential.
var ErrBadKey = errors.New("tmdb: the key was refused")

const (
	baseURL          = "https://api.themoviedb.org/3"
	imageBase        = "https://image.tmdb.org/t/p/original"
	maxResponseBytes = 32 << 20
)

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks TMDB. Credential is read on every call so a key saved in the interface applies at once; it is
// either the v4 read access token (a JWT, sent as a bearer) or the v3 API key (sent as a parameter).
type Client struct {
	http       Doer
	credential func() string
	language   string
	base       string
}

// New builds a client over an httpx client carrying TMDB's own rate bucket; language is a BCP 47 tag, "en-US" if empty.
func New(client Doer, credential func() string, language string) Client {
	if language == "" {
		language = "en-US"
	}
	return Client{http: client, credential: credential, language: language, base: baseURL}
}

// Configured reports whether a credential is stored.
func (c Client) Configured() bool {
	return c.credential != nil && strings.TrimSpace(c.credential()) != ""
}

// ImageURL turns a TMDB file path into the full-size picture address; empty stays empty.
func ImageURL(path string) string {
	if path == "" {
		return ""
	}
	return imageBase + path
}

// Genre is TMDB's id-and-name pair, used for genres and keywords.
type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Network is a broadcaster or streamer with its logo.
type Network struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	LogoPath      string `json:"logo_path"`
	OriginCountry string `json:"origin_country"`
}

// Creator is a series' creator.
type Creator struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ProfilePath string `json:"profile_path"`
}

// SeasonSummary is a season as the series answer lists it.
type SeasonSummary struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	AirDate      string `json:"air_date"`
	EpisodeCount int    `json:"episode_count"`
	PosterPath   string `json:"poster_path"`
}

// Role is one character a person played across the series, with the episode count.
type Role struct {
	Character    string `json:"character"`
	EpisodeCount int    `json:"episode_count"`
}

// Job is one crew job a person held across the series, with the episode count.
type Job struct {
	Job          string `json:"job"`
	EpisodeCount int    `json:"episode_count"`
}

// CastMember is an aggregate-credits cast entry.
type CastMember struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	ProfilePath       string `json:"profile_path"`
	Order             int    `json:"order"`
	TotalEpisodeCount int    `json:"total_episode_count"`
	Roles             []Role `json:"roles"`
}

// CrewMember is an aggregate-credits crew entry.
type CrewMember struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	ProfilePath       string `json:"profile_path"`
	Department        string `json:"department"`
	TotalEpisodeCount int    `json:"total_episode_count"`
	Jobs              []Job  `json:"jobs"`
}

// Image is one picture with the language it is captioned in (empty for none).
type Image struct {
	FilePath    string  `json:"file_path"`
	Language    string  `json:"iso_639_1"`
	VoteAverage float64 `json:"vote_average"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
}

// EpisodeGroupSummary is an alternative ordering a series offers: DVD, absolute, production and so on.
type EpisodeGroupSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         int    `json:"type"`
	EpisodeCount int    `json:"episode_count"`
	GroupCount   int    `json:"group_count"`
}

// Series is one show with everything the appended answers carry.
type Series struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	OriginalName     string          `json:"original_name"`
	Overview         string          `json:"overview"`
	FirstAirDate     string          `json:"first_air_date"`
	LastAirDate      string          `json:"last_air_date"`
	Status           string          `json:"status"`
	Type             string          `json:"type"`
	EpisodeRunTime   []int           `json:"episode_run_time"`
	OriginCountry    []string        `json:"origin_country"`
	OriginalLanguage string          `json:"original_language"`
	VoteAverage      float64         `json:"vote_average"`
	VoteCount        int             `json:"vote_count"`
	PosterPath       string          `json:"poster_path"`
	BackdropPath     string          `json:"backdrop_path"`
	Genres           []Genre         `json:"genres"`
	Networks         []Network       `json:"networks"`
	Companies        []Network       `json:"production_companies"`
	CreatedBy        []Creator       `json:"created_by"`
	Seasons          []SeasonSummary `json:"seasons"`
	ExternalIDs      struct {
		IMDb   string `json:"imdb_id"`
		TVDB   int64  `json:"tvdb_id"`
		TVRage int64  `json:"tvrage_id"`
	} `json:"external_ids"`
	AggregateCredits struct {
		Cast []CastMember `json:"cast"`
		Crew []CrewMember `json:"crew"`
	} `json:"aggregate_credits"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
	Keywords struct {
		Results []Genre `json:"results"`
	} `json:"keywords"`
	Images struct {
		Posters   []Image `json:"posters"`
		Backdrops []Image `json:"backdrops"`
		Logos     []Image `json:"logos"`
	} `json:"images"`
	AlternativeTitles struct {
		Results []struct {
			Title   string `json:"title"`
			Country string `json:"iso_3166_1"`
		} `json:"results"`
	} `json:"alternative_titles"`
	EpisodeGroups struct {
		Results []EpisodeGroupSummary `json:"results"`
	} `json:"episode_groups"`
}

// ContentRating answers the certification for a country, "US" when asked for none, else the first listed.
func (s Series) ContentRating(country string) string {
	for _, wanted := range []string{country, "US"} {
		for _, entry := range s.ContentRatings.Results {
			if wanted != "" && entry.Country == wanted && entry.Rating != "" {
				return entry.Rating
			}
		}
	}
	for _, entry := range s.ContentRatings.Results {
		if entry.Rating != "" {
			return entry.Rating
		}
	}
	return ""
}

const seriesAppend = "aggregate_credits,external_ids,content_ratings,keywords,images,alternative_titles,episode_groups"

// Series reads one show by TMDB id with its credits, external ids, ratings, keywords, artwork, titles and orderings.
func (c Client) Series(ctx context.Context, id int64) (Series, error) {
	var series Series
	query := url.Values{"append_to_response": {seriesAppend}, "include_image_language": {c.imageLanguages()}}
	return series, c.get(ctx, "/tv/"+strconv.FormatInt(id, 10), query, &series)
}

func (c Client) imageLanguages() string {
	short, _, _ := strings.Cut(c.language, "-")
	return short + ",null"
}

// GuestStar is an episode's guest cast entry.
type GuestStar struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	ProfilePath string `json:"profile_path"`
	Order       int    `json:"order"`
}

// EpisodeCrew is one crew member of a single episode.
type EpisodeCrew struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Job         string `json:"job"`
	Department  string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

// Episode is one episode with its own guest stars and crew.
type Episode struct {
	ID            int64         `json:"id"`
	SeasonNumber  int           `json:"season_number"`
	EpisodeNumber int           `json:"episode_number"`
	Name          string        `json:"name"`
	Overview      string        `json:"overview"`
	AirDate       string        `json:"air_date"`
	Runtime       int           `json:"runtime"`
	StillPath     string        `json:"still_path"`
	GuestStars    []GuestStar   `json:"guest_stars"`
	Crew          []EpisodeCrew `json:"crew"`
}

// Season is one season with its episodes.
type Season struct {
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	AirDate      string    `json:"air_date"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

// Season reads one season's episodes, each with guest stars and crew.
func (c Client) Season(ctx context.Context, seriesID int64, number int) (Season, error) {
	var season Season
	return season, c.get(ctx, "/tv/"+strconv.FormatInt(seriesID, 10)+"/season/"+strconv.Itoa(number), nil, &season)
}

// Hit is one search or find result.
type Hit struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	OriginalName     string   `json:"original_name"`
	Overview         string   `json:"overview"`
	FirstAirDate     string   `json:"first_air_date"`
	PosterPath       string   `json:"poster_path"`
	OriginCountry    []string `json:"origin_country"`
	OriginalLanguage string   `json:"original_language"`
	GenreIDs         []int64  `json:"genre_ids"`
}

// GenreNames are TMDB's genre ids by name, film and TV alike; the lists are fixed and published.
var GenreNames = map[int64]string{
	28: "Action", 12: "Adventure", 16: "Animation", 35: "Comedy", 80: "Crime", 99: "Documentary", 18: "Drama",
	10751: "Family", 14: "Fantasy", 36: "History", 27: "Horror", 10402: "Music", 9648: "Mystery", 10749: "Romance",
	878: "Science Fiction", 10770: "TV Movie", 53: "Thriller", 10752: "War", 37: "Western",
	10759: "Action & Adventure", 10762: "Kids", 10763: "News", 10764: "Reality", 10765: "Sci-Fi & Fantasy",
	10766: "Soap", 10767: "Talk", 10768: "War & Politics",
}

// Genres names a hit's genre ids; an id TMDB adds later is left out.
func Genres(ids []int64) []string {
	var names []string
	for _, id := range ids {
		if name, known := GenreNames[id]; known {
			names = append(names, name)
		}
	}
	return names
}

// Search finds shows by name.
func (c Client) Search(ctx context.Context, term string) ([]Hit, error) {
	var answer struct {
		Results []Hit `json:"results"`
	}
	return answer.Results, c.get(ctx, "/search/tv", url.Values{"query": {term}}, &answer)
}

// NewOnNetwork lists the shows that first aired on a network on or after a date (yyyy-MM-dd), newest first.
func (c Client) NewOnNetwork(ctx context.Context, networkID int64, since string) ([]Hit, error) {
	var answer struct {
		Results []Hit `json:"results"`
	}
	query := url.Values{
		"with_networks": {strconv.FormatInt(networkID, 10)}, "sort_by": {"first_air_date.desc"}, "first_air_date.gte": {since},
	}
	return answer.Results, c.get(ctx, "/discover/tv", query, &answer)
}

// FindByTVDB resolves a TheTVDB id to TMDB's show; ErrNotFound when TMDB maps none.
func (c Client) FindByTVDB(ctx context.Context, tvdbID int64) (Hit, error) {
	return c.find(ctx, strconv.FormatInt(tvdbID, 10), "tvdb_id")
}

// FindByIMDb resolves an IMDb id (tt...) to TMDB's show; ErrNotFound when TMDB maps none.
func (c Client) FindByIMDb(ctx context.Context, imdbID string) (Hit, error) {
	return c.find(ctx, imdbID, "imdb_id")
}

func (c Client) find(ctx context.Context, id, source string) (Hit, error) {
	var answer struct {
		Results []Hit `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(id), url.Values{"external_source": {source}}, &answer); err != nil {
		return Hit{}, err
	}
	if len(answer.Results) == 0 {
		return Hit{}, ErrNotFound
	}
	return answer.Results[0], nil
}

// GroupEpisode places one episode in an alternative ordering.
type GroupEpisode struct {
	ID            int64 `json:"id"`
	Order         int   `json:"order"`
	SeasonNumber  int   `json:"season_number"`
	EpisodeNumber int   `json:"episode_number"`
}

// EpisodeGroup is one alternative ordering with its groups (seasons, in that ordering).
type EpisodeGroup struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Groups []struct {
		Name     string         `json:"name"`
		Order    int            `json:"order"`
		Episodes []GroupEpisode `json:"episodes"`
	} `json:"groups"`
}

// EpisodeGroup reads one alternative ordering by its id.
func (c Client) EpisodeGroup(ctx context.Context, id string) (EpisodeGroup, error) {
	var group EpisodeGroup
	return group, c.get(ctx, "/tv/episode_group/"+url.PathEscape(id), nil, &group)
}

// Credit is one title a person appears in, as the person page lists it.
type Credit struct {
	ID           int64  `json:"id"`
	MediaType    string `json:"media_type"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Character    string `json:"character"`
	Job          string `json:"job"`
	FirstAirDate string `json:"first_air_date"`
	ReleaseDate  string `json:"release_date"`
	PosterPath   string `json:"poster_path"`
	EpisodeCount int    `json:"episode_count"`
}

// Person is one human with the biography and every credit TMDB lists.
type Person struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Biography          string   `json:"biography"`
	Birthday           string   `json:"birthday"`
	Deathday           string   `json:"deathday"`
	PlaceOfBirth       string   `json:"place_of_birth"`
	ProfilePath        string   `json:"profile_path"`
	Gender             int      `json:"gender"`
	KnownForDepartment string   `json:"known_for_department"`
	AlsoKnownAs        []string `json:"also_known_as"`
	ExternalIDs        struct {
		IMDb     string `json:"imdb_id"`
		Wikidata string `json:"wikidata_id"`
	} `json:"external_ids"`
	CombinedCredits struct {
		Cast []Credit `json:"cast"`
		Crew []Credit `json:"crew"`
	} `json:"combined_credits"`
}

// Person reads one person by TMDB id with every credit they have.
func (c Client) Person(ctx context.Context, id int64) (Person, error) {
	var person Person
	query := url.Values{"append_to_response": {"combined_credits,external_ids"}}
	return person, c.get(ctx, "/person/"+strconv.FormatInt(id, 10), query, &person)
}

// Ping asks the cheapest authenticated question, for the connection test.
func (c Client) Ping(ctx context.Context) error {
	return c.get(ctx, "/configuration", nil, &struct{}{})
}

func (c Client) get(ctx context.Context, path string, query url.Values, into any) error {
	key := ""
	if c.credential != nil {
		key = strings.TrimSpace(c.credential())
	}
	if key == "" {
		return ErrNoKey
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("language", c.language)
	bearer := strings.HasPrefix(key, "eyJ")
	if !bearer {
		query.Set("api_key", key)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("tmdb: %s: %w", path, err)
	}
	if bearer {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		var refused *httpx.StatusError
		if errors.As(err, &refused) {
			switch refused.Status {
			case http.StatusNotFound:
				return ErrNotFound
			case http.StatusUnauthorized:
				return ErrBadKey
			}
		}
		return fmt.Errorf("tmdb: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized:
		return ErrBadKey
	default:
		return fmt.Errorf("tmdb: %s: status %d", path, response.StatusCode)
	}
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("tmdb: %s: read: %w", path, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("tmdb: %s: decode: %w", path, err)
	}
	return nil
}
