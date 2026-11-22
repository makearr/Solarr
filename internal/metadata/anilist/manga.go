package anilist

import (
	"context"
	"encoding/json"
	"fmt"
)

const mangaFields = `id idMal title{romaji english native} synonyms format chapters volumes status startDate{year} countryOfOrigin isAdult description(asHtml:false) genres tags{name isAdult} ` +
	`coverImage{extraLarge} staff(perPage:12){edges{role node{name{full}}}}`

const (
	mangaQuery       = `query($id:Int){Media(id:$id,type:MANGA){` + mangaFields + `}}`
	mangaSearchQuery = `query($search:String,$page:Int,$perPage:Int){Page(page:$page,perPage:$perPage){pageInfo{hasNextPage}media(search:$search,type:MANGA,sort:SEARCH_MATCH){` + mangaFields + `}}}`
)

// MangaPageSize is how many entries one page of a manga search holds.
const MangaPageSize = 20

// Manga is one manga, one-shot or novel entry.
type Manga struct {
	ID    int64 `json:"id"`
	IDMal int64 `json:"idMal"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	Synonyms  []string `json:"synonyms"`
	Format    string   `json:"format"`
	Chapters  int      `json:"chapters"`
	Volumes   int      `json:"volumes"`
	Status    string   `json:"status"`
	StartDate struct {
		Year int `json:"year"`
	} `json:"startDate"`
	CountryOfOrigin string   `json:"countryOfOrigin"`
	IsAdult         bool     `json:"isAdult"`
	Description     string   `json:"description"`
	Genres          []string `json:"genres"`
	Tags            []struct {
		Name    string `json:"name"`
		IsAdult bool   `json:"isAdult"`
	} `json:"tags"`
	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
	} `json:"coverImage"`
	Staff struct {
		Edges []struct {
			Role string `json:"role"`
			Node struct {
				Name struct {
					Full string `json:"full"`
				} `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"staff"`
}

// Titles lists every name the entry answers to, the three titles first, without repeats.
func (m Manga) Titles() []string {
	return Media{Title: m.Title, Synonyms: m.Synonyms}.Titles()
}

// MangaPerson is one credit of a manga: the role AniList words ("Story & Art", "Story", "Art").
type MangaPerson struct{ Name, Role string }

// People lists the credited staff.
func (m Manga) People() []MangaPerson {
	var out []MangaPerson
	for _, e := range m.Staff.Edges {
		out = append(out, MangaPerson{Name: e.Node.Name.Full, Role: e.Role})
	}
	return out
}

// Manga reads one manga by AniList id.
func (c Client) Manga(ctx context.Context, id int64) (Manga, error) {
	raw, err := c.post(ctx, mangaQuery, id)
	if err != nil {
		return Manga{}, err
	}
	var answer struct {
		Data struct {
			Media *Manga `json:"Media"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return Manga{}, fmt.Errorf("anilist: %d: decode: %w", id, err)
	}
	if err := refusal(id, answer.Errors); err != nil {
		return Manga{}, err
	}
	if answer.Data.Media == nil {
		return Manga{}, ErrNotFound
	}
	return *answer.Data.Media, nil
}

// SearchManga looks manga up by title, MangaPageSize at a time from page 1; more is true while another page follows.
func (c Client) SearchManga(ctx context.Context, title string, page int) (found []Manga, more bool, err error) {
	raw, err := c.ask(ctx, mangaSearchQuery, map[string]any{"search": title, "page": max(page, 1), "perPage": MangaPageSize})
	if err != nil {
		return nil, false, fmt.Errorf("anilist: search: %w", err)
	}
	var answer struct {
		Data struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []Manga `json:"media"`
			} `json:"Page"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, false, fmt.Errorf("anilist: search: decode: %w", err)
	}
	if len(answer.Errors) > 0 {
		return nil, false, fmt.Errorf("anilist: search: %s", messages(answer.Errors))
	}
	return answer.Data.Page.Media, answer.Data.Page.PageInfo.HasNextPage, nil
}
