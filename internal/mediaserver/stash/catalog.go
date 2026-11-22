package stash

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// boxes are the stash-box servers a scene may be linked to, by the provider name a scene's ids carry.
var boxes = map[string]string{
	"stashdb":   "https://stashdb.org/graphql",
	"theporndb": "https://theporndb.net/graphql",
	"fansdb":    "https://fansdb.cc/graphql",
}

const sceneFields = `id title details date code director studio { name } performers { name } tags { name } stash_ids { endpoint stash_id } files { path duration }`

type wireScene struct {
	ID         string                  `json:"id"`
	Title      string                  `json:"title"`
	Details    string                  `json:"details"`
	Date       string                  `json:"date"`
	Code       string                  `json:"code"`
	Director   string                  `json:"director"`
	Studio     *struct{ Name string }  `json:"studio"`
	Performers []struct{ Name string } `json:"performers"`
	Tags       []struct{ Name string } `json:"tags"`
	StashIDs   []struct {
		Endpoint string `json:"endpoint"`
		StashID  string `json:"stash_id"`
	} `json:"stash_ids"`
	Files []struct {
		Path     string  `json:"path"`
		Duration float64 `json:"duration"`
	} `json:"files"`
}

func (wire wireScene) item() mediaserver.Item {
	item := mediaserver.Item{ID: wire.ID, MediaType: "adult", Title: wire.Title, Overview: wire.Details, Date: wire.Date, IDs: map[string]string{}}
	if len(wire.Date) >= 4 {
		item.Year, _ = strconv.Atoi(wire.Date[:4])
	}
	if wire.Code != "" {
		item.IDs["code"] = wire.Code
	}
	if wire.Studio != nil && wire.Studio.Name != "" {
		item.Organisations = []string{wire.Studio.Name}
	}
	for _, tag := range wire.Tags {
		item.Tags = append(item.Tags, tag.Name)
	}
	for _, performer := range wire.Performers {
		item.People = append(item.People, mediaserver.Credit{Name: performer.Name, Kind: "actor"})
	}
	if wire.Director != "" {
		item.People = append(item.People, mediaserver.Credit{Name: wire.Director, Kind: "director"})
	}
	for _, link := range wire.StashIDs {
		for name, endpoint := range boxes {
			if link.Endpoint == endpoint {
				item.IDs[name] = link.StashID
			}
		}
	}
	if len(wire.Files) > 0 {
		item.Path, item.RuntimeMS = wire.Files[0].Path, int64(wire.Files[0].Duration*1000)
	}
	return item
}

func (c *Client) scenes(ctx context.Context, filter string, limit int) ([]wireScene, error) {
	var reply struct {
		Data struct {
			FindScenes struct {
				Scenes []wireScene `json:"scenes"`
			} `json:"findScenes"`
		} `json:"data"`
	}
	query := fmt.Sprintf(`{ findScenes(%s, filter: {per_page: %d}) { scenes { %s } } }`, filter, limit, sceneFields)
	if err := c.query(ctx, query, &reply); err != nil {
		return nil, err
	}
	return reply.Data.FindScenes.Scenes, nil
}

// ItemByProviderID finds the scene linked to the stash-box id; a provider that is no stash-box finds none.
func (c *Client) ItemByProviderID(ctx context.Context, _, provider, id string) (mediaserver.Item, bool, error) {
	endpoint, known := boxes[provider]
	if !known {
		return mediaserver.Item{}, false, nil
	}
	filter := fmt.Sprintf(`scene_filter: {stash_id_endpoint: {endpoint: %s, stash_id: %s, modifier: EQUALS}}`, strconv.Quote(endpoint), strconv.Quote(id))
	found, err := c.scenes(ctx, filter, 1)
	if err != nil || len(found) == 0 {
		return mediaserver.Item{}, false, err
	}
	return found[0].item(), true, nil
}

// ItemByPath finds the scene whose file is serverPath.
func (c *Client) ItemByPath(ctx context.Context, serverPath string) (mediaserver.Item, bool, error) {
	filter := fmt.Sprintf(`scene_filter: {path: {value: %s, modifier: INCLUDES}}`, strconv.Quote(path.Base(serverPath)))
	found, err := c.scenes(ctx, filter, 10)
	if err != nil {
		return mediaserver.Item{}, false, err
	}
	for _, scene := range found {
		for _, file := range scene.Files {
			if file.Path == serverPath {
				return scene.item(), true, nil
			}
		}
	}
	return mediaserver.Item{}, false, nil
}

// Search finds scenes by Stash's own text search.
func (c *Client) Search(ctx context.Context, _, query string, limit int) ([]mediaserver.Item, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	found, err := c.scenes(ctx, fmt.Sprintf(`scene_filter: {title: {value: %s, modifier: INCLUDES}}`, strconv.Quote(strings.TrimSpace(query))), limit)
	if err != nil {
		return nil, err
	}
	items := make([]mediaserver.Item, len(found))
	for index, scene := range found {
		items[index] = scene.item()
	}
	return items, nil
}

// Item reads one scene by its id.
func (c *Client) Item(ctx context.Context, id string) (mediaserver.Item, error) {
	number, err := strconv.Atoi(id)
	if err != nil {
		return mediaserver.Item{}, mediaserver.ErrItemNotFound
	}
	found, err := c.scenes(ctx, `scene_ids: [`+strconv.Itoa(number)+`]`, 1)
	if err != nil {
		return mediaserver.Item{}, err
	}
	if len(found) == 0 {
		return mediaserver.Item{}, mediaserver.ErrItemNotFound
	}
	return found[0].item(), nil
}
