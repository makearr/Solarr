package audiobookshelf

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// listPage is how many items one request of the library listing reads.
const listPage = 200

type wireBook struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Media struct {
		Duration float64 `json:"duration"`
		Metadata struct {
			Title       string   `json:"title"`
			Subtitle    string   `json:"subtitle"`
			AuthorName  string   `json:"authorName"`
			Narrator    string   `json:"narratorName"`
			SeriesName  string   `json:"seriesName"`
			Genres      []string `json:"genres"`
			Year        string   `json:"publishedYear"`
			Publisher   string   `json:"publisher"`
			Description string   `json:"description"`
			ISBN        string   `json:"isbn"`
			ASIN        string   `json:"asin"`
			Language    string   `json:"language"`
		} `json:"metadata"`
	} `json:"media"`
}

// shelf is every book of the server's book libraries, read once for the life of the client: the server has no lookup by id or path.
type shelf struct {
	once  sync.Once
	items []mediaserver.Item
	err   error
}

func (c *Client) toItem(in wireBook) mediaserver.Item {
	meta := in.Media.Metadata
	item := mediaserver.Item{
		ID: in.ID, MediaType: "book", Title: meta.Title, Overview: meta.Description, Genres: meta.Genres, Language: meta.Language,
		RuntimeMS: int64(in.Media.Duration * 1000), Path: in.Path, IDs: map[string]string{},
	}
	item.Year, _ = strconv.Atoi(meta.Year)
	if meta.ISBN != "" {
		item.IDs["isbn"] = meta.ISBN
	}
	if meta.ASIN != "" {
		item.IDs["asin"] = meta.ASIN
	}
	if meta.Publisher != "" {
		item.Organisations = []string{meta.Publisher}
	}
	for _, name := range splitNames(meta.AuthorName) {
		item.People = append(item.People, mediaserver.Credit{Name: name, Kind: "author"})
	}
	for _, name := range splitNames(strings.TrimPrefix(meta.Narrator, "Narrator:")) {
		item.People = append(item.People, mediaserver.Credit{Name: name, Kind: "narrator"})
	}
	if meta.SeriesName != "" {
		item.Parent = &mediaserver.ItemRef{Title: meta.SeriesName}
	}
	return item
}

func splitNames(list string) []string {
	var names []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func (c *Client) books(ctx context.Context) ([]mediaserver.Item, error) {
	c.shelf.once.Do(func() {
		libraries, err := c.Libraries(ctx)
		if err != nil {
			c.shelf.err = err
			return
		}
		for _, library := range libraries {
			if library.Type != "book" {
				continue
			}
			for page := 0; ; page++ {
				address := mediaserver.Join(c.base, "/api/libraries/"+url.PathEscape(library.ID)+"/items?limit="+strconv.Itoa(listPage)+"&page="+strconv.Itoa(page))
				var reply struct {
					Results []wireBook `json:"results"`
				}
				if err := mediaserver.Call(ctx, c.doer, http.MethodGet, address, c.header(), nil, &reply); err != nil {
					c.shelf.err, c.shelf.items = mediaserver.Refused(err), nil
					return
				}
				for _, in := range reply.Results {
					c.shelf.items = append(c.shelf.items, c.toItem(in))
				}
				if len(reply.Results) < listPage {
					break
				}
			}
		}
	})
	return c.shelf.items, c.shelf.err
}

// ItemByProviderID finds the book that carries the ASIN or ISBN.
func (c *Client) ItemByProviderID(ctx context.Context, _, provider, id string) (mediaserver.Item, bool, error) {
	items, err := c.books(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	for _, item := range items {
		if item.IDs[provider] == id {
			return item, true, nil
		}
	}
	return mediaserver.Item{}, false, nil
}

// ItemByPath finds the book whose folder or file is serverPath or above it.
func (c *Client) ItemByPath(ctx context.Context, serverPath string) (mediaserver.Item, bool, error) {
	items, err := c.books(ctx)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	for _, item := range items {
		if item.Path != "" && (item.Path == serverPath || strings.HasPrefix(serverPath, strings.TrimRight(item.Path, "/")+"/")) {
			return item, true, nil
		}
	}
	return mediaserver.Item{}, false, nil
}

// Search finds the books whose title holds the query, case aside.
func (c *Client) Search(ctx context.Context, _, query string, limit int) ([]mediaserver.Item, error) {
	items, err := c.books(ctx)
	if err != nil {
		return nil, err
	}
	var found []mediaserver.Item
	for _, item := range items {
		if len(found) < limit && strings.Contains(strings.ToLower(item.Title), strings.ToLower(strings.TrimSpace(query))) {
			found = append(found, item)
		}
	}
	return found, nil
}

// Item reads one book by its id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	items, err := c.books(ctx)
	if err != nil {
		return mediaserver.Item{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return mediaserver.Item{}, mediaserver.ErrItemNotFound
}
