// Package mangadex reads MangaDex (mangadex.org) as metadata only: titles, content rating, which chapters belong to which volume, and covers per volume.
// Solarr never downloads chapters from it. MangaDex's terms ask for a credit and a real User-Agent, forbid a Via header and require covers to be proxied (the remote-image cache does).
package mangadex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ErrNotFound means MangaDex knows no manga under the id.
var ErrNotFound = errors.New("mangadex: not found")

const (
	baseURL          = "https://api.mangadex.org"
	coverBase        = "https://uploads.mangadex.org/covers/"
	maxResponseBytes = 4 << 20
	// PageSize is how many manga one search page lists.
	PageSize = 20
	// UnnumberedVolume is the key MangaDex gives chapters that belong to no volume.
	UnnumberedVolume = "none"
)

// Content ratings MangaDex gives a manga.
const (
	RatingSafe         = "safe"
	RatingSuggestive   = "suggestive"
	RatingErotica      = "erotica"
	RatingPornographic = "pornographic"
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks MangaDex.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying MangaDex's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Manga is one MangaDex entry.
type Manga struct {
	ID    string
	Title string
	// AltTitles are every other title in every language, without repeats.
	AltTitles        []string
	Description      string
	OriginalLanguage string
	Status           string
	Year             int
	// LastVolume is the number MangaDex gives the final volume of a finished series; 0 when unknown.
	LastVolume    int
	ContentRating string
	Demographic   string
	Tags          []string
	// Links are the cross-references by MangaDex's keys: "al" (AniList id), "mu" (MangaUpdates code), "mal" (MyAnimeList id).
	Links map[string]string
}

// Volume is one volume of the aggregate and the chapter numbers it holds.
type Volume struct {
	// Number is the volume as MangaDex prints it ("1", "2.5"), or UnnumberedVolume.
	Number   string
	Chapters []string
}

// Cover is one volume's cover image.
type Cover struct {
	Volume string
	URL    string
	Locale string
}

type mangaJSON struct {
	ID         string `json:"id"`
	Attributes struct {
		Title            map[string]string   `json:"title"`
		AltTitles        []map[string]string `json:"altTitles"`
		Description      map[string]string   `json:"description"`
		OriginalLanguage string              `json:"originalLanguage"`
		Status           string              `json:"status"`
		Year             int                 `json:"year"`
		LastVolume       string              `json:"lastVolume"`
		ContentRating    string              `json:"contentRating"`
		Demographic      string              `json:"publicationDemographic"`
		Links            map[string]string   `json:"links"`
		Tags             []struct {
			Attributes struct {
				Name map[string]string `json:"name"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
}

func firstOf(texts map[string]string, preferred ...string) string {
	for _, lang := range preferred {
		if t := texts[lang]; t != "" {
			return t
		}
	}
	keys := make([]string, 0, len(texts))
	for k := range texts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if texts[k] != "" {
			return texts[k]
		}
	}
	return ""
}

func (m mangaJSON) manga() Manga {
	a := m.Attributes
	title := firstOf(a.Title, "en")
	out := Manga{ID: m.ID, Title: title, Description: firstOf(a.Description, "en"), OriginalLanguage: a.OriginalLanguage, Status: a.Status, Year: a.Year,
		ContentRating: a.ContentRating, Demographic: a.Demographic, Links: a.Links}
	out.LastVolume, _ = strconv.Atoi(a.LastVolume)
	seen := map[string]bool{title: true}
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out.AltTitles = append(out.AltTitles, t)
		}
	}
	langs := make([]string, 0, len(a.Title))
	for lang := range a.Title {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		add(a.Title[lang])
	}
	for _, alt := range a.AltTitles {
		keys := make([]string, 0, len(alt))
		for k := range alt {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add(alt[k])
		}
	}
	for _, t := range a.Tags {
		if name := firstOf(t.Attributes.Name, "en"); name != "" {
			out.Tags = append(out.Tags, name)
		}
	}
	return out
}

// SearchManga looks manga up by title, PageSize at a time from page 1, limited to the given content ratings (all when none); more is true while another page follows.
func (c Client) SearchManga(ctx context.Context, title string, page int, ratings []string) (found []Manga, more bool, err error) {
	query := url.Values{"title": {title}, "limit": {strconv.Itoa(PageSize)}, "offset": {strconv.Itoa((max(page, 1) - 1) * PageSize)}}
	for _, r := range ratings {
		query.Add("contentRating[]", r)
	}
	var answer struct {
		Data  []mangaJSON `json:"data"`
		Total int         `json:"total"`
	}
	if err := c.http.GetJSON(ctx, c.base+"/manga?"+query.Encode(), maxResponseBytes, &answer); err != nil {
		return nil, false, fmt.Errorf("mangadex: %w", err)
	}
	for _, m := range answer.Data {
		found = append(found, m.manga())
	}
	return found, max(page, 1)*PageSize < answer.Total, nil
}

// Manga reads one manga by its UUID.
func (c Client) Manga(ctx context.Context, id string) (Manga, error) {
	var answer struct {
		Data mangaJSON `json:"data"`
	}
	if err := c.http.GetJSON(ctx, c.base+"/manga/"+url.PathEscape(id), maxResponseBytes, &answer); err != nil {
		return Manga{}, fmt.Errorf("mangadex: %w", err)
	}
	if answer.Data.ID == "" {
		return Manga{}, ErrNotFound
	}
	return answer.Data.manga(), nil
}

// Aggregate lists the volumes of a manga with their chapters, in numeric volume order and the unnumbered chapters last, for the translations in language ("en").
func (c Client) Aggregate(ctx context.Context, id, language string) ([]Volume, error) {
	query := url.Values{"translatedLanguage[]": {language}}
	var answer struct {
		Volumes map[string]struct {
			Volume   string `json:"volume"`
			Chapters map[string]struct {
				Chapter string `json:"chapter"`
			} `json:"chapters"`
		} `json:"volumes"`
	}
	if err := c.http.GetJSON(ctx, c.base+"/manga/"+url.PathEscape(id)+"/aggregate?"+query.Encode(), maxResponseBytes, &answer); err != nil {
		return nil, fmt.Errorf("mangadex: %w", err)
	}
	var out []Volume
	for key, v := range answer.Volumes {
		volume := Volume{Number: key}
		for _, ch := range v.Chapters {
			volume.Chapters = append(volume.Chapters, ch.Chapter)
		}
		sort.Slice(volume.Chapters, func(i, j int) bool { return numberLess(volume.Chapters[i], volume.Chapters[j]) })
		out = append(out, volume)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Number == UnnumberedVolume) != (out[j].Number == UnnumberedVolume) {
			return out[j].Number == UnnumberedVolume
		}
		return numberLess(out[i].Number, out[j].Number)
	})
	return out, nil
}

// numberLess orders "2" before "10" and "2" before "2.5"; text that is no number sorts after numbers.
func numberLess(a, b string) bool {
	fa, errA := strconv.ParseFloat(strings.TrimSpace(a), 64)
	fb, errB := strconv.ParseFloat(strings.TrimSpace(b), 64)
	switch {
	case errA != nil && errB != nil:
		return a < b
	case errA != nil:
		return false
	case errB != nil:
		return true
	default:
		return fa < fb
	}
}

// Covers lists a manga's covers, one per volume where MangaDex has them.
func (c Client) Covers(ctx context.Context, id string) ([]Cover, error) {
	query := url.Values{"manga[]": {id}, "limit": {"100"}, "order[volume]": {"asc"}}
	var answer struct {
		Data []struct {
			Attributes struct {
				Volume   string `json:"volume"`
				FileName string `json:"fileName"`
				Locale   string `json:"locale"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.http.GetJSON(ctx, c.base+"/cover?"+query.Encode(), maxResponseBytes, &answer); err != nil {
		return nil, fmt.Errorf("mangadex: %w", err)
	}
	var out []Cover
	for _, d := range answer.Data {
		if d.Attributes.FileName == "" {
			continue
		}
		out = append(out, Cover{Volume: d.Attributes.Volume, Locale: d.Attributes.Locale, URL: coverBase + url.PathEscape(id) + "/" + url.PathEscape(d.Attributes.FileName)})
	}
	return out, nil
}
