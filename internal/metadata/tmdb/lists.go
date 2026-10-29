package tmdb

import (
	"context"
	"net/url"
	"strconv"
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
	var answer struct {
		Results    []ListItem `json:"results"`
		TotalPages int        `json:"total_pages"`
	}
	if err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}}, &answer); err != nil {
		return ListPage{}, err
	}
	for i := range answer.Results {
		answer.Results[i].MediaType = media
	}
	return ListPage{Items: answer.Results, TotalPages: answer.TotalPages}, nil
}
