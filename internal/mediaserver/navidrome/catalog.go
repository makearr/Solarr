package navidrome

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type wireAlbum struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Artist        string `json:"artist"`
	Year          int    `json:"year"`
	Duration      int64  `json:"duration"`
	MusicBrainzID string `json:"musicBrainzId"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	RecordLabels []struct {
		Name string `json:"name"`
	} `json:"recordLabels"`
	ReleaseDate struct {
		Year, Month, Day int
	} `json:"releaseDate"`
}

func (wire wireAlbum) item() mediaserver.Item {
	item := mediaserver.Item{
		ID: wire.ID, MediaType: "music", Title: wire.Name, Year: wire.Year, RuntimeMS: wire.Duration * 1000, IDs: map[string]string{},
	}
	if wire.MusicBrainzID != "" {
		item.IDs["musicbrainzalbum"] = wire.MusicBrainzID
	}
	if wire.Artist != "" {
		item.People = []mediaserver.Credit{{Name: wire.Artist, Kind: "artist"}}
	}
	for _, genre := range wire.Genres {
		item.Genres = append(item.Genres, genre.Name)
	}
	for _, label := range wire.RecordLabels {
		item.Organisations = append(item.Organisations, label.Name)
	}
	if day := wire.ReleaseDate; day.Year > 0 && day.Month > 0 && day.Day > 0 {
		item.Date = strconv.Itoa(day.Year) + "-" + pad(day.Month) + "-" + pad(day.Day)
	}
	return item
}

func pad(number int) string {
	if number < 10 {
		return "0" + strconv.Itoa(number)
	}
	return strconv.Itoa(number)
}

// Search finds albums by Navidrome's own search of the text.
func (c *Client) Search(ctx context.Context, _, query string, limit int) ([]mediaserver.Item, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	reply, err := c.call(ctx, "search3", url.Values{"query": {query}, "albumCount": {strconv.Itoa(limit)}, "artistCount": {"0"}, "songCount": {"0"}})
	if err != nil {
		return nil, mediaserver.Refused(err)
	}
	items := make([]mediaserver.Item, len(reply.SearchResult3.Album))
	for index, album := range reply.SearchResult3.Album {
		items[index] = album.item()
	}
	return items, nil
}

// Item reads one album by its id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	reply, err := c.call(ctx, "getAlbum", url.Values{"id": {id}})
	if err != nil {
		return mediaserver.Item{}, mediaserver.Refused(err)
	}
	if reply.Album.ID == "" {
		return mediaserver.Item{}, mediaserver.ErrItemNotFound
	}
	return reply.Album.item(), nil
}

// ItemByPath never matches: Navidrome answers song paths relative to its music folder, which no Solarr path maps onto.
func (c *Client) ItemByPath(context.Context, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}
