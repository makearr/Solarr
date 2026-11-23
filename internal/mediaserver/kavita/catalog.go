package kavita

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type wireName struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type wireMetadata struct {
	Summary      string     `json:"summary"`
	Genres       []wireName `json:"genres"`
	Tags         []wireName `json:"tags"`
	Writers      []wireName `json:"writers"`
	CoverArtists []wireName `json:"coverArtists"`
	Pencillers   []wireName `json:"pencillers"`
	Inkers       []wireName `json:"inkers"`
	Colorists    []wireName `json:"colorists"`
	Letterers    []wireName `json:"letterers"`
	Editors      []wireName `json:"editors"`
	Translators  []wireName `json:"translators"`
	Publishers   []wireName `json:"publishers"`
	Imprints     []wireName `json:"imprints"`
	ReleaseYear  int        `json:"releaseYear"`
	Language     string     `json:"language"`
	// WebLinks is the series' addresses, comma separated.
	WebLinks string `json:"webLinks"`
}

type wireSeries struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	Name          string `json:"name"`
	OriginalName  string `json:"originalName"`
	LocalizedName string `json:"localizedName"`
	SortName      string `json:"sortName"`
	ReleaseYear   int    `json:"releaseYear"`
	FolderPath    string `json:"folderPath"`
}

// The series' own addresses at the registries carry the ids Solarr keys a series by.
var linkIDs = []struct {
	provider string
	pattern  *regexp.Regexp
}{
	{"comicvine", regexp.MustCompile(`comicvine\.gamespot\.com/.*?/4050-(\d+)`)},
	{"anilist", regexp.MustCompile(`anilist\.co/manga/(\d+)`)},
	{"mal", regexp.MustCompile(`myanimelist\.net/manga/(\d+)`)},
	{"mangadex", regexp.MustCompile(`mangadex\.org/title/([0-9a-f-]{36})`)},
	{"mangaupdates", regexp.MustCompile(`mangaupdates\.com/series(?:/|\.html\?id=)(\w+)`)},
}

func names(wire []wireName) []string {
	var out []string
	for _, one := range wire {
		if name := strings.TrimSpace(one.Name + one.Title); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func (m wireMetadata) item(series wireSeries, mediaType string) mediaserver.Item {
	id := series.ID
	if id == 0 {
		id = series.SeriesID
	}
	item := mediaserver.Item{
		ID: strconv.Itoa(id), MediaType: mediaType, Title: series.Name, OriginalTitle: series.OriginalName, SortTitle: series.SortName,
		Overview: strings.TrimSpace(m.Summary), Year: m.ReleaseYear, Language: m.Language, Genres: names(m.Genres), Tags: names(m.Tags),
		IDs: map[string]string{}, Organisations: append(names(m.Publishers), names(m.Imprints)...), Path: series.FolderPath,
	}
	if item.Year == 0 {
		item.Year = series.ReleaseYear
	}
	for kind, list := range map[string][]wireName{
		"writer": m.Writers, "cover_artist": m.CoverArtists, "penciller": m.Pencillers, "inker": m.Inkers, "colorist": m.Colorists,
		"letterer": m.Letterers, "editor": m.Editors, "translator": m.Translators,
	} {
		for _, name := range names(list) {
			item.People = append(item.People, mediaserver.Credit{Name: name, Kind: kind})
		}
	}
	sortCredits(item.People)
	for _, link := range strings.Split(m.WebLinks, ",") {
		for _, known := range linkIDs {
			if found := known.pattern.FindStringSubmatch(link); found != nil && item.IDs[known.provider] == "" {
				item.IDs[known.provider] = found[1]
			}
		}
	}
	return item
}

// creditRank puts the writers first, then the artists, so a series' lead is its writer.
var creditRank = map[string]int{"writer": 0, "penciller": 1, "inker": 2, "colorist": 3, "letterer": 4, "cover_artist": 5, "editor": 6, "translator": 7}

func sortCredits(people []mediaserver.Credit) {
	slices.SortFunc(people, func(a, b mediaserver.Credit) int {
		return cmp.Or(cmp.Compare(creditRank[a.Kind], creditRank[b.Kind]), strings.Compare(a.Name, b.Name))
	})
}

// Search finds series by Kavita's own search of the text; each hit is read in full so a caller sees the same item Item gives.
func (c *Client) Search(ctx context.Context, mediaType, query string, limit int) ([]mediaserver.Item, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	var found struct {
		Series []wireSeries `json:"series"`
	}
	address := mediaserver.Join(c.base, "/api/Search/search") + mediaserver.Query(url.Values{"queryString": {strings.TrimSpace(query)}})
	if err := c.call(ctx, http.MethodGet, address, nil, &found); err != nil {
		return nil, mediaserver.Refused(err)
	}
	items := make([]mediaserver.Item, 0, min(limit, len(found.Series)))
	for _, series := range found.Series {
		if len(items) == limit {
			break
		}
		var metadata wireMetadata
		address := mediaserver.Join(c.base, "/api/Series/metadata") + mediaserver.Query(url.Values{"seriesId": {strconv.Itoa(series.SeriesID)}})
		if err := c.call(ctx, http.MethodGet, address, nil, &metadata); err != nil {
			return nil, mediaserver.Refused(err)
		}
		items = append(items, metadata.item(series, mediaType))
	}
	return items, nil
}

// Item reads one series by its Kavita id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	var series wireSeries
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/Series/"+url.PathEscape(id)), nil, &series); err != nil {
		return mediaserver.Item{}, mediaserver.Refused(err)
	}
	var metadata wireMetadata
	address := mediaserver.Join(c.base, "/api/Series/metadata") + mediaserver.Query(url.Values{"seriesId": {id}})
	if err := c.call(ctx, http.MethodGet, address, nil, &metadata); err != nil {
		return mediaserver.Item{}, mediaserver.Refused(err)
	}
	return metadata.item(series, ""), nil
}

// ItemByPath never matches: Kavita offers no lookup by folder, so a series is found by its title instead.
func (c *Client) ItemByPath(context.Context, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}

// ItemByProviderID never matches: Kavita cannot search by the ids in a series' links; the title decides.
func (c *Client) ItemByProviderID(context.Context, string, string, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}
