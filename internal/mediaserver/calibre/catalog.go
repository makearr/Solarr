package calibre

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

type wireBook struct {
	ID          int64             `json:"application_id"`
	Title       string            `json:"title"`
	Authors     []string          `json:"authors"`
	Comments    string            `json:"comments"`
	Tags        []string          `json:"tags"`
	Publisher   string            `json:"publisher"`
	Languages   []string          `json:"languages"`
	PubDate     string            `json:"pubdate"`
	Series      string            `json:"series"`
	SeriesIndex float64           `json:"series_index"`
	Identifiers map[string]string `json:"identifiers"`
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func (wire wireBook) item() mediaserver.Item {
	item := mediaserver.Item{
		ID: strconv.FormatInt(wire.ID, 10), MediaType: "book", Title: wire.Title, Overview: strings.TrimSpace(htmlTag.ReplaceAllString(wire.Comments, " ")),
		Genres: wire.Tags, IDs: make(map[string]string, len(wire.Identifiers)),
	}
	if len(wire.Languages) > 0 {
		item.Language = wire.Languages[0]
	}
	if len(wire.PubDate) >= 4 {
		item.Year, _ = strconv.Atoi(wire.PubDate[:4])
		if len(wire.PubDate) >= 10 && item.Year > 1 {
			item.Date = wire.PubDate[:10]
		}
	}
	if wire.Publisher != "" {
		item.Organisations = []string{wire.Publisher}
	}
	if wire.Series != "" {
		item.Parent = &mediaserver.ItemRef{Title: wire.Series + " #" + strconv.FormatFloat(wire.SeriesIndex, 'f', -1, 64)}
	}
	for _, author := range wire.Authors {
		item.People = append(item.People, mediaserver.Credit{Name: author, Kind: "author"})
	}
	for name, id := range wire.Identifiers {
		if id != "" {
			item.IDs[strings.ToLower(name)] = id
		}
	}
	// Calibre names the Amazon id "amazon"; the book module calls it the ASIN.
	if id := item.IDs["amazon"]; id != "" && item.IDs["asin"] == "" {
		item.IDs["asin"] = id
	}
	return item
}

func (c *Client) books(ctx context.Context, query string, limit int) ([]mediaserver.Item, error) {
	var found struct {
		IDs []int64 `json:"book_ids"`
	}
	if err := c.get(ctx, "/ajax/search?num="+strconv.Itoa(limit)+"&query="+url.QueryEscape(query), &found); err != nil {
		return nil, mediaserver.Refused(err)
	}
	if len(found.IDs) == 0 {
		return nil, nil
	}
	ids := make([]string, len(found.IDs))
	for index, id := range found.IDs {
		ids[index] = strconv.FormatInt(id, 10)
	}
	var byID map[string]wireBook
	if err := c.get(ctx, "/ajax/books?ids="+strings.Join(ids, ","), &byID); err != nil {
		return nil, mediaserver.Refused(err)
	}
	items := make([]mediaserver.Item, 0, len(ids))
	for _, id := range ids {
		if wire, ok := byID[id]; ok {
			wire.ID, _ = strconv.ParseInt(id, 10, 64)
			items = append(items, wire.item())
		}
	}
	return items, nil
}

// ItemByProviderID finds the book that carries the identifier (isbn, goodreads, google, amazon).
func (c *Client) ItemByProviderID(ctx context.Context, _, provider, id string) (mediaserver.Item, bool, error) {
	if provider == "asin" {
		provider = "amazon"
	}
	found, err := c.books(ctx, fmt.Sprintf("identifiers:%s:%s", provider, id), 1)
	if err != nil || len(found) == 0 {
		return mediaserver.Item{}, false, err
	}
	return found[0], true, nil
}

// Search finds books by Calibre's own search of the text.
func (c *Client) Search(ctx context.Context, _, query string, limit int) ([]mediaserver.Item, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	return c.books(ctx, "title:"+strconv.Quote(strings.TrimSpace(query)), limit)
}

// Item reads one book by its Calibre id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	found, err := c.books(ctx, "id:"+id, 1)
	if err != nil {
		return mediaserver.Item{}, err
	}
	if len(found) == 0 {
		return mediaserver.Item{}, mediaserver.ErrItemNotFound
	}
	return found[0], nil
}

// ItemByPath never matches: Calibre keeps its own folder layout, so a file Solarr manages is found by identifier or title instead.
func (c *Client) ItemByPath(context.Context, string) (mediaserver.Item, bool, error) {
	return mediaserver.Item{}, false, nil
}
