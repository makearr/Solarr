package jellyfin

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// itemFields are what Jellyfin is asked to include; its Path filter answers every item, so a file is found by name.
const itemFields = "ProviderIds,Overview,Genres,People,Studios,OriginalTitle,OfficialRating,Path,Tags,ProductionYear,PremiereDate,RunTimeTicks,SortName,CommunityRating,OriginalLanguage"

var itemTypes = map[string]string{
	"movie": "Movie", "tv": "Series", "music": "MusicAlbum,MusicArtist,Audio", "audiobook": "AudioBook",
}

type wireItem struct {
	ID            string            `json:"Id"`
	Type          string            `json:"Type"`
	Name          string            `json:"Name"`
	OriginalTitle string            `json:"OriginalTitle"`
	SortName      string            `json:"SortName"`
	Overview      string            `json:"Overview"`
	Year          int               `json:"ProductionYear"`
	Premiere      string            `json:"PremiereDate"`
	Genres        []string          `json:"Genres"`
	Tags          []string          `json:"Tags"`
	Rating        float64           `json:"CommunityRating"`
	OfficialClass string            `json:"OfficialRating"`
	Language      string            `json:"OriginalLanguage"`
	RunTimeTicks  int64             `json:"RunTimeTicks"`
	ProviderIDs   map[string]string `json:"ProviderIds"`
	Path          string            `json:"Path"`
	People        []struct {
		Name string `json:"Name"`
		Role string `json:"Role"`
		Type string `json:"Type"`
	} `json:"People"`
	Studios []struct {
		Name string `json:"Name"`
	} `json:"Studios"`
	ImageTags map[string]string `json:"ImageTags"`
	Index     int               `json:"IndexNumber"`
	ParentIdx int               `json:"ParentIndexNumber"`
	SeriesID  string            `json:"SeriesId"`
	Series    string            `json:"SeriesName"`
	AlbumID   string            `json:"AlbumId"`
	Album     string            `json:"Album"`
}

func (c *Client) toItem(in wireItem) mediaserver.Item {
	item := mediaserver.Item{
		ID: in.ID, MediaType: mediaTypeOf(in.Type), Title: in.Name, OriginalTitle: in.OriginalTitle, SortTitle: in.SortName,
		Overview: in.Overview, Year: in.Year, Genres: in.Genres, Tags: in.Tags, Rating: in.Rating, AgeRating: in.OfficialClass,
		Language: in.Language, RuntimeMS: in.RunTimeTicks / 10000, Index: in.Index, ParentIndex: in.ParentIdx, Path: in.Path,
		IDs: make(map[string]string, len(in.ProviderIDs)),
	}
	if day, _, found := strings.Cut(in.Premiere, "T"); found && len(day) == 10 {
		item.Date = day
	}
	for name, id := range in.ProviderIDs {
		if id != "" {
			item.IDs[strings.ToLower(name)] = id
		}
	}
	for _, person := range in.People {
		item.People = append(item.People, mediaserver.Credit{Name: person.Name, Role: person.Role, Kind: strings.ToLower(person.Type)})
	}
	for _, studio := range in.Studios {
		item.Organisations = append(item.Organisations, studio.Name)
	}
	for kind := range in.ImageTags {
		item.Images = append(item.Images, mediaserver.Image{Kind: strings.ToLower(kind), URL: mediaserver.Join(c.base, "/Items/"+url.PathEscape(in.ID)+"/Images/"+url.PathEscape(kind))})
	}
	switch {
	case in.SeriesID != "":
		item.Parent = &mediaserver.ItemRef{ID: in.SeriesID, Title: in.Series}
	case in.AlbumID != "":
		item.Parent = &mediaserver.ItemRef{ID: in.AlbumID, Title: in.Album}
	}
	return item
}

func mediaTypeOf(kind string) string {
	switch kind {
	case "Movie":
		return "movie"
	case "Series", "Season", "Episode":
		return "tv"
	case "MusicAlbum", "MusicArtist", "Audio":
		return "music"
	case "AudioBook":
		return "audiobook"
	}
	return ""
}

func (c *Client) items(ctx context.Context, query url.Values) ([]mediaserver.Item, error) {
	query.Set("Recursive", "true")
	query.Set("Fields", itemFields)
	query.Set("EnableUserData", "false")
	var page struct {
		Items []wireItem `json:"Items"`
	}
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/Items?"+query.Encode()), nil, &page); err != nil {
		return nil, mediaserver.Refused(err)
	}
	out := make([]mediaserver.Item, 0, len(page.Items))
	for _, in := range page.Items {
		out = append(out, c.toItem(in))
	}
	return out, nil
}

// Search finds items by name among the media type's kinds of item.
func (c *Client) Search(ctx context.Context, mediaType, query string, limit int) ([]mediaserver.Item, error) {
	kinds, known := itemTypes[mediaType]
	if !known || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	return c.items(ctx, url.Values{"searchTerm": {query}, "IncludeItemTypes": {kinds}, "Limit": {strconv.Itoa(limit)}})
}

// Item reads one item by its id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	found, err := c.items(ctx, url.Values{"Ids": {id}, "Limit": {"1"}})
	if err != nil {
		return mediaserver.Item{}, err
	}
	if len(found) == 0 {
		return mediaserver.Item{}, mediaserver.ErrItemNotFound
	}
	return found[0], nil
}

// ItemByPath finds the film, series or album a file belongs to: the server is searched by the file's name and by the folder's,
// and an item counts only when its path is the file or a folder above it.
func (c *Client) ItemByPath(ctx context.Context, serverPath string) (mediaserver.Item, bool, error) {
	file := strings.TrimSuffix(path.Base(serverPath), path.Ext(serverPath))
	folder := path.Base(path.Dir(serverPath))
	for _, name := range []string{file, folder, path.Base(path.Dir(path.Dir(serverPath)))} {
		term := searchName(name)
		if term == "" {
			continue
		}
		found, err := c.items(ctx, url.Values{"searchTerm": {term}, "IncludeItemTypes": {"Movie,Series,MusicAlbum"}, "Limit": {"10"}})
		if err != nil {
			return mediaserver.Item{}, false, err
		}
		for _, item := range found {
			if item.Path != "" && (item.Path == serverPath || strings.HasPrefix(serverPath, strings.TrimRight(item.Path, "/")+"/")) {
				return item, true, nil
			}
		}
	}
	return mediaserver.Item{}, false, nil
}

// searchName is a file or folder name as a person would type the title: the year and bracketed tags cut off.
func searchName(name string) string {
	for _, open := range []string{" (", " [", " - "} {
		if at := strings.Index(name, open); at > 0 {
			name = name[:at]
		}
	}
	return strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(name))
}
