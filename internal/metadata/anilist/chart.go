package anilist

import (
	"context"
	"encoding/json"
	"fmt"
)

// The sorts a chart reads by.
const (
	SortTrending   = "TRENDING_DESC"
	SortScore      = "SCORE_DESC"
	SortPopularity = "POPULARITY_DESC"
)

// StatusNotYetReleased is AniList's word for an anime that has not begun airing.
const StatusNotYetReleased = "NOT_YET_RELEASED"

const chartQuery = `query($page:Int,$perPage:Int,$sort:[MediaSort],$status:MediaStatus,$before:FuzzyDateInt,$notGenres:[String],$minPopularity:Int)` +
	`{Page(page:$page,perPage:$perPage){pageInfo{hasNextPage}media(type:ANIME,isAdult:false,sort:$sort,status:$status,startDate_lesser:$before,` +
	`genre_not_in:$notGenres,popularity_greater:$minPopularity){` + mediaFields + `}}}`

// ChartQuery picks a chart; zero fields leave their filter off.
type ChartQuery struct {
	Sort   string
	Status string
	// StartedBefore is a date as AniList writes it, YYYYMMDD; 0 for any.
	StartedBefore int
	ExcludeGenres []string
	// MinPopularity is the number of lists an anime must be on, so a handful of scores cannot top a chart.
	MinPopularity int
	// PerPage is how many entries a page holds, at most SeasonPageSize; 0 asks the most.
	PerPage int
}

// Chart reads one page of anime ranked as the query says; more says another page follows.
func (c Client) Chart(ctx context.Context, chart ChartQuery, page int) (entries []Media, more bool, err error) {
	perPage := SeasonPageSize
	if chart.PerPage > 0 && chart.PerPage < perPage {
		perPage = chart.PerPage
	}
	variables := map[string]any{"page": page, "perPage": perPage, "sort": []string{chart.Sort}}
	if chart.Status != "" {
		variables["status"] = chart.Status
	}
	if chart.StartedBefore > 0 {
		variables["before"] = chart.StartedBefore
	}
	if len(chart.ExcludeGenres) > 0 {
		variables["notGenres"] = chart.ExcludeGenres
	}
	if chart.MinPopularity > 0 {
		variables["minPopularity"] = chart.MinPopularity
	}
	raw, err := c.ask(ctx, chartQuery, variables)
	if err != nil {
		return nil, false, fmt.Errorf("anilist: chart %s: %w", chart.Sort, err)
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
		return nil, false, fmt.Errorf("anilist: chart %s: decode: %w", chart.Sort, err)
	}
	if answer.Data.Page == nil {
		return nil, false, fmt.Errorf("anilist: chart %s: %s", chart.Sort, messages(answer.Errors))
	}
	return answer.Data.Page.Media, answer.Data.Page.PageInfo.HasNextPage, nil
}
