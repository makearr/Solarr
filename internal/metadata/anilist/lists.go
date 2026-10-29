package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrUnknownUser means AniList has no user by that name, or keeps the user's list private.
var ErrUnknownUser = errors.New("anilist: no such user, or the list is private")

// SeasonPageSize is how many entries one page of a season holds; AniList allows at most 50.
const SeasonPageSize = 50

// The four seasons AniList groups anime by.
const (
	SeasonWinter = "WINTER"
	SeasonSpring = "SPRING"
	SeasonSummer = "SUMMER"
	SeasonFall   = "FALL"
)

const seasonQuery = `query($season:MediaSeason,$year:Int,$page:Int,$perPage:Int){Page(page:$page,perPage:$perPage){pageInfo{hasNextPage}` +
	`media(season:$season,seasonYear:$year,type:ANIME,isAdult:false,sort:POPULARITY_DESC){` + mediaFields + `}}}`

const userListQuery = `query($name:String){MediaListCollection(userName:$name,type:ANIME){lists{entries{status media{` + mediaFields + `}}}}}`

// graphQLError is one of the errors AniList answers beside, or instead of, its data.
type graphQLError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// Season reads one page of a season's anime, most popular first; more says another page follows.
func (c Client) Season(ctx context.Context, season string, year, page int) (entries []Media, more bool, err error) {
	raw, err := c.ask(ctx, seasonQuery, map[string]any{"season": season, "year": year, "page": page, "perPage": SeasonPageSize})
	if err != nil {
		return nil, false, fmt.Errorf("anilist: season %s %d: %w", season, year, err)
	}
	var answer struct {
		Data struct {
			Page *struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []Media `json:"media"`
			} `json:"Page"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, false, fmt.Errorf("anilist: season %s %d: decode: %w", season, year, err)
	}
	if answer.Data.Page == nil {
		return nil, false, fmt.Errorf("anilist: season %s %d: %s", season, year, messages(answer.Errors))
	}
	return answer.Data.Page.Media, answer.Data.Page.PageInfo.HasNextPage, nil
}

// ListedMedia is one entry of a user's list, with the status the user gave it (CURRENT, PLANNING, COMPLETED, ...).
type ListedMedia struct {
	Status string
	Media  Media
}

// UserList reads a user's whole anime list, every status.
func (c Client) UserList(ctx context.Context, userName string) ([]ListedMedia, error) {
	raw, err := c.ask(ctx, userListQuery, map[string]any{"name": userName})
	if errors.Is(err, ErrNotFound) {
		return nil, ErrUnknownUser
	}
	if err != nil {
		return nil, fmt.Errorf("anilist: list of %q: %w", userName, err)
	}
	var answer struct {
		Data struct {
			Collection *struct {
				Lists []struct {
					Entries []struct {
						Status string `json:"status"`
						Media  Media  `json:"media"`
					} `json:"entries"`
				} `json:"lists"`
			} `json:"MediaListCollection"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("anilist: list of %q: decode: %w", userName, err)
	}
	if answer.Data.Collection == nil {
		for _, refused := range answer.Errors {
			if refused.Status == 404 || strings.Contains(strings.ToLower(refused.Message), "not found") {
				return nil, ErrUnknownUser
			}
		}
		return nil, fmt.Errorf("anilist: list of %q: %s", userName, messages(answer.Errors))
	}
	var listed []ListedMedia
	for _, list := range answer.Data.Collection.Lists {
		for _, entry := range list.Entries {
			listed = append(listed, ListedMedia{Status: entry.Status, Media: entry.Media})
		}
	}
	return listed, nil
}

func messages(errs []graphQLError) string {
	if len(errs) == 0 {
		return "no data in the answer"
	}
	texts := make([]string, len(errs))
	for i, refused := range errs {
		texts[i] = refused.Message
	}
	return strings.Join(texts, "; ")
}
