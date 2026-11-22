// Package jellyfin is the client for a Jellyfin server, signed in with an API key from its dashboard.
package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Jellyfin server.
type Client struct {
	doer mediaserver.Doer
	base string
	key  string
}

// New builds a client for the server at base with the API key.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

func (c *Client) header() http.Header {
	return http.Header{"Authorization": {fmt.Sprintf("MediaBrowser Token=%q", c.key)}}
}

// Test reads the public server facts, then asks for the library list, which only an administrator's key may do.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	var public struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/System/Info/Public"), nil, nil, &public); err != nil {
		return mediaserver.Info{}, err
	}
	info := mediaserver.Info{Name: public.ServerName, Version: public.Version}
	_, err := c.Libraries(ctx)
	switch {
	case err == nil:
		info.Admin = true
	case isNotAdmin(err):
		return info, nil
	default:
		return info, err
	}
	return info, nil
}

func isNotAdmin(err error) bool { return err != nil && errors.Is(err, mediaserver.ErrNotAdmin) }

// Libraries lists the virtual folders (libraries) with their paths.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var folders []struct {
		Name           string   `json:"Name"`
		ItemID         string   `json:"ItemId"`
		CollectionType string   `json:"CollectionType"`
		Locations      []string `json:"Locations"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/Library/VirtualFolders"), c.header(), nil, &folders); err != nil {
		return nil, mediaserver.Refused(err)
	}
	libraries := make([]mediaserver.Library, len(folders))
	for index, folder := range folders {
		libraries[index] = mediaserver.Library{ID: folder.ItemID, Name: folder.Name, Type: folder.CollectionType, Paths: folder.Locations}
	}
	return libraries, nil
}

// Users lists the accounts.
func (c *Client) Users(ctx context.Context) ([]mediaserver.User, error) {
	var accounts []struct {
		ID   string `json:"Id"`
		Name string `json:"Name"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/Users"), c.header(), nil, &accounts); err != nil {
		return nil, mediaserver.Refused(err)
	}
	users := make([]mediaserver.User, len(accounts))
	for index, account := range accounts {
		users[index] = mediaserver.User{ID: account.ID, Name: account.Name}
	}
	return users, nil
}

// maxTargetedPaths is how many paths of one batch are told one by one before the whole library is scanned instead.
const maxTargetedPaths = 50

// Changed tells the server of the paths in one call, or scans when there are many.
func (c *Client) Changed(ctx context.Context, changes []mediaserver.Change) error {
	if len(changes) == 0 {
		return nil
	}
	if len(changes) > maxTargetedPaths {
		return mediaserver.Call(ctx, c.doer, http.MethodPost, mediaserver.Join(c.base, "/Library/Refresh"), c.header(), nil, nil)
	}
	type update struct {
		Path       string `json:"Path"`
		UpdateType string `json:"UpdateType"`
	}
	updates := make([]update, len(changes))
	for index, change := range changes {
		updates[index] = update{Path: change.Path, UpdateType: updateType(change.Kind)}
	}
	body := struct {
		Updates []update `json:"Updates"`
	}{updates}
	return mediaserver.Call(ctx, c.doer, http.MethodPost, mediaserver.Join(c.base, "/Library/Media/Updated"), c.header(), body, nil)
}

func updateType(kind string) string {
	switch kind {
	case "created":
		return "Created"
	case "deleted":
		return "Deleted"
	default:
		return "Modified"
	}
}

// Announce shows a message in every active session that can be remote controlled.
func (c *Client) Announce(ctx context.Context, text string) error {
	var sessions []struct {
		ID                    string `json:"Id"`
		IsActive              bool   `json:"IsActive"`
		SupportsRemoteControl bool   `json:"SupportsRemoteControl"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/Sessions"), c.header(), nil, &sessions); err != nil {
		return err
	}
	message := struct {
		Header    string `json:"Header"`
		Text      string `json:"Text"`
		TimeoutMs int    `json:"TimeoutMs"`
	}{Header: "Solarr", Text: text, TimeoutMs: 8000}
	for _, session := range sessions {
		if !session.IsActive || !session.SupportsRemoteControl {
			continue
		}
		address := mediaserver.Join(c.base, "/Sessions/"+url.PathEscape(session.ID)+"/Message")
		if err := mediaserver.Call(ctx, c.doer, http.MethodPost, address, c.header(), message, nil); err != nil {
			return err
		}
	}
	return nil
}
