package jellyfin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// indexPage is how many items one request of the id index reads.
const indexPage = 1000

// idIndex maps "provider:id" to the server's item id for one media type; Jellyfin ignores an id filter, so the ids are read once.
type idIndex struct {
	once sync.Once
	byID map[string]string
	err  error
}

func (c *Client) indexOf(mediaType string) *idIndex {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	if c.indexes == nil {
		c.indexes = map[string]*idIndex{}
	}
	if c.indexes[mediaType] == nil {
		c.indexes[mediaType] = &idIndex{}
	}
	return c.indexes[mediaType]
}

func (c *Client) loadIndex(ctx context.Context, mediaType string) (map[string]string, error) {
	index := c.indexOf(mediaType)
	index.once.Do(func() {
		kinds, known := itemTypes[mediaType]
		if !known {
			return
		}
		index.byID = map[string]string{}
		for start := 0; ; start += indexPage {
			query := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {kinds}, "Fields": {"ProviderIds"}, "EnableUserData": {"false"},
				"StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(indexPage)}, "EnableImages": {"false"}}
			var page struct {
				Items []wireItem `json:"Items"`
			}
			if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/Items?"+query.Encode()), nil, &page); err != nil {
				index.err = mediaserver.Refused(err)
				index.byID = nil
				return
			}
			for _, in := range page.Items {
				for name, id := range in.ProviderIDs {
					if id != "" {
						index.byID[strings.ToLower(name)+":"+id] = in.ID
					}
				}
			}
			if len(page.Items) < indexPage {
				return
			}
		}
	})
	return index.byID, index.err
}

// ItemByProviderID finds the item of a media type that carries the provider's id.
func (c *Client) ItemByProviderID(ctx context.Context, mediaType, provider, id string) (mediaserver.Item, bool, error) {
	byID, err := c.loadIndex(ctx, mediaType)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	serverID, found := byID[provider+":"+id]
	if !found {
		return mediaserver.Item{}, false, nil
	}
	item, err := c.Item(ctx, serverID)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	return item, true, nil
}
