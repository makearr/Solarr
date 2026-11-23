package tmdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// The two kinds of entry a TMDB list or chart holds, as TMDB names them.
const (
	MediaTV    = "tv"
	MediaMovie = "movie"
)

// The windows TMDB's trending chart is counted over.
const (
	TrendingDay  = "day"
	TrendingWeek = "week"
)

// ListItem is one entry of a TMDB list or chart: a show (Name, FirstAirDate) or a film (Title, ReleaseDate).
type ListItem struct {
	ID           int64  `json:"id"`
	MediaType    string `json:"media_type"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	FirstAirDate string `json:"first_air_date"`
	ReleaseDate  string `json:"release_date"`
	PosterPath   string `json:"poster_path"`
	// What a chart row also carries; a list row leaves them empty.
	Overview         string   `json:"overview"`
	GenreIDs         []int64  `json:"genre_ids"`
	OriginalLanguage string   `json:"original_language"`
	OriginCountry    []string `json:"origin_country"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int64    `json:"vote_count"`
	Popularity       float64  `json:"popularity"`
}

// ListPage is one page of a list or chart, and how many pages it has.
type ListPage struct {
	Items      []ListItem
	TotalPages int
}

// List reads one page of a public list by its id.
func (c Client) List(ctx context.Context, listID string, page int) (ListPage, error) {
	var answer struct {
		Items      []ListItem `json:"items"`
		TotalPages int        `json:"total_pages"`
	}
	err := c.get(ctx, "/list/"+url.PathEscape(listID), url.Values{"page": {strconv.Itoa(page)}}, &answer)
	return ListPage{Items: answer.Items, TotalPages: answer.TotalPages}, err
}

// Trending reads one page of what is trending of a kind (MediaTV, MediaMovie) over a window.
func (c Client) Trending(ctx context.Context, media, window string, page int) (ListPage, error) {
	return c.chart(ctx, "/trending/"+url.PathEscape(media)+"/"+url.PathEscape(window), media, page)
}

// Popular reads one page of the most popular of a kind (MediaTV, MediaMovie).
func (c Client) Popular(ctx context.Context, media string, page int) (ListPage, error) {
	return c.chart(ctx, "/"+url.PathEscape(media)+"/popular", media, page)
}

// chart reads a chart page, whose entries are all of one kind and may not say so.
func (c Client) chart(ctx context.Context, path, media string, page int) (ListPage, error) {
	return c.chartWith(ctx, path, media, url.Values{"page": {strconv.Itoa(page)}})
}

func (c Client) chartWith(ctx context.Context, path, media string, values url.Values) (ListPage, error) {
	var answer struct {
		Results    []ListItem `json:"results"`
		TotalPages int        `json:"total_pages"`
	}
	if err := c.get(ctx, path, values, &answer); err != nil {
		return ListPage{}, err
	}
	for i := range answer.Results {
		answer.Results[i].MediaType = media
	}
	return ListPage{Items: answer.Results, TotalPages: answer.TotalPages}, nil
}

// The orders a discover query sorts by.
const (
	SortPopularity = "popularity.desc"
	SortRating     = "vote_average.desc"
)

// DiscoverQuery narrows TMDB's discover chart; what TMDB cannot push down the caller checks itself.
type DiscoverQuery struct {
	SortBy   string
	MinVotes int
	// ReleasedFrom and ReleasedTo are ISO dates on the first release (a film's primary release, a show's first air date); empty is open.
	ReleasedFrom, ReleasedTo string
	WithoutGenres            []int64
	// Language is one original language; empty is any.
	Language string
}

// Discover reads one page of the discover chart of a kind (MediaTV, MediaMovie).
func (c Client) Discover(ctx context.Context, media string, query DiscoverQuery, page int) (ListPage, error) {
	dateField := "primary_release_date"
	if media == MediaTV {
		dateField = "first_air_date"
	}
	values := url.Values{"page": {strconv.Itoa(page)}, "include_adult": {"false"}, "sort_by": {query.SortBy}}
	if query.MinVotes > 0 {
		values.Set("vote_count.gte", strconv.Itoa(query.MinVotes))
	}
	if query.ReleasedFrom != "" {
		values.Set(dateField+".gte", query.ReleasedFrom)
	}
	if query.ReleasedTo != "" {
		values.Set(dateField+".lte", query.ReleasedTo)
	}
	if len(query.WithoutGenres) > 0 {
		ids := make([]string, len(query.WithoutGenres))
		for index, id := range query.WithoutGenres {
			ids[index] = strconv.FormatInt(id, 10)
		}
		values.Set("without_genres", strings.Join(ids, "|"))
	}
	if query.Language != "" {
		values.Set("with_original_language", query.Language)
	}
	return c.chartWith(ctx, "/discover/"+url.PathEscape(media), media, values)
}
