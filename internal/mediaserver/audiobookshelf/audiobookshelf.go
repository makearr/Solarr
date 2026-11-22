// Package audiobookshelf is the client for an Audiobookshelf server, signed in with a user's API token.
package audiobookshelf

import (
	"context"
	"net/http"
	"net/url"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Audiobookshelf server.
type Client struct {
	doer  mediaserver.Doer
	base  string
	token string
}

// New builds a client for the server at base with the token.
func New(doer mediaserver.Doer, base, token string) *Client {
	return &Client{doer: doer, base: base, token: token}
}

func (c *Client) header() http.Header { return http.Header{"Authorization": {"Bearer " + c.token}} }

// Test reads the version (public) and the token's own user, whose type says whether a scan is allowed.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	var status struct {
		ServerVersion string `json:"serverVersion"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/status"), nil, nil, &status); err != nil {
		return mediaserver.Info{}, err
	}
	var me struct {
		Type string `json:"type"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/me"), c.header(), nil, &me); err != nil {
		return mediaserver.Info{}, mediaserver.Refused(err)
	}
	return mediaserver.Info{Name: "Audiobookshelf", Version: status.ServerVersion, Admin: me.Type == "admin" || me.Type == "root"}, nil
}

// Libraries lists the libraries with their folders.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var reply struct {
		Libraries []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			MediaType string `json:"mediaType"`
			Folders   []struct {
				FullPath string `json:"fullPath"`
			} `json:"folders"`
		} `json:"libraries"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/libraries"), c.header(), nil, &reply); err != nil {
		return nil, mediaserver.Refused(err)
	}
	libraries := make([]mediaserver.Library, len(reply.Libraries))
	for index, library := range reply.Libraries {
		paths := make([]string, len(library.Folders))
		for position, folder := range library.Folders {
			paths[position] = folder.FullPath
		}
		libraries[index] = mediaserver.Library{ID: library.ID, Name: library.Name, Type: library.MediaType, Paths: paths}
	}
	return libraries, nil
}

// Users lists the accounts; only an administrator's token may.
func (c *Client) Users(ctx context.Context) ([]mediaserver.User, error) {
	var reply struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"users"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/users"), c.header(), nil, &reply); err != nil {
		return nil, mediaserver.Refused(err)
	}
	users := make([]mediaserver.User, len(reply.Users))
	for index, user := range reply.Users {
		users[index] = mediaserver.User{ID: user.ID, Name: user.Username}
	}
	return users, nil
}

// Changed scans each library the changes belong to once; the server has no per-folder call to rely on.
func (c *Client) Changed(ctx context.Context, changes []mediaserver.Change) error {
	for _, id := range mediaserver.LibraryIDsOf(changes) {
		address := mediaserver.Join(c.base, "/api/libraries/"+url.PathEscape(id)+"/scan")
		if err := mediaserver.Call(ctx, c.doer, http.MethodPost, address, c.header(), nil, nil); err != nil {
			return err
		}
	}
	return nil
}
