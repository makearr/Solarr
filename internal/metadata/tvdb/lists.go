package tvdb

import (
	"context"
	"net/url"
	"strconv"
)

// List is a list a series is on: TheTVDB's own official ones name a franchise ("Arrowverse"), the rest are users'.
type List struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IsOfficial bool   `json:"isOfficial"`
}

// SeriesLists reads the lists a series is on, from its short extended answer.
func (c Client) SeriesLists(ctx context.Context, id int64) ([]List, error) {
	var series struct {
		Lists []List `json:"lists"`
	}
	err := c.get(ctx, "/series/"+strconv.FormatInt(id, 10)+"/extended", url.Values{"short": {"true"}}, &series)
	return series.Lists, err
}

// ListEntry is one title of a list: a series or a film, by TheTVDB id (0 for the other).
type ListEntry struct {
	SeriesID int64 `json:"seriesId"`
	MovieID  int64 `json:"movieId"`
	Order    int   `json:"order"`
}

// ListEntries reads the titles of one list.
func (c Client) ListEntries(ctx context.Context, id int64) ([]ListEntry, error) {
	var list struct {
		Entities []ListEntry `json:"entities"`
	}
	err := c.get(ctx, "/lists/"+strconv.FormatInt(id, 10)+"/extended", nil, &list)
	return list.Entities, err
}
