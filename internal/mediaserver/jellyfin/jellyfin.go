// Package jellyfin is the client for a Jellyfin server, signed in with an API key from its dashboard.
package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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
