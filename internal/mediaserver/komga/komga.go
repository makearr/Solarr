// Package komga is the client for a Komga server, signed in with an API key (sent as X-API-Key) or with a user name and password (HTTP basic).
package komga

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Komga server.
type Client struct {
	doer mediaserver.Doer
	base string
	key  string
	// basic is the Authorization value of a connection that signs in with a user name and password; the key is then empty.
	basic string
}

// New builds a client for the server at base with the API key.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

// NewLogin builds a client that sends username and password as HTTP basic credentials, which is all Komga asks.
func NewLogin(doer mediaserver.Doer, base, username, password string) *Client {
	return &Client{doer: doer, base: base, basic: "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))}
}

func (c *Client) header() http.Header {
	if c.basic != "" {
		return http.Header{"Authorization": {c.basic}}
	}
	return http.Header{"X-Api-Key": {c.key}}
}

func (c *Client) call(ctx context.Context, method, address string, into any) error {
	return mediaserver.Refused(mediaserver.Call(ctx, c.doer, method, address, c.header(), nil, into))
}

// Test reads the signed-in user, whose roles say whether the credential is an administrator's, then the version.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	var me struct {
		Roles []string `json:"roles"`
	}
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/v2/users/me"), &me); err != nil {
		return mediaserver.Info{}, err
	}
	info := mediaserver.Info{Name: "Komga", Admin: slices.Contains(me.Roles, "ADMIN")}
	var about struct {
		Build struct {
			Version string `json:"version"`
		} `json:"build"`
	}
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/actuator/info"), &about); err == nil {
		info.Version = about.Build.Version
	}
	return info, nil
}

// Libraries lists the libraries with their root folder.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var reply []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Root string `json:"root"`
	}
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/v1/libraries"), &reply); err != nil {
		return nil, err
	}
	libraries := make([]mediaserver.Library, len(reply))
	for index, library := range reply {
		libraries[index] = mediaserver.Library{ID: library.ID, Name: library.Name, Type: "comic", Paths: []string{library.Root}}
	}
	return libraries, nil
}

// Changed starts a scan of each library the changes belong to, once.
func (c *Client) Changed(ctx context.Context, changes []mediaserver.Change) error {
	for _, id := range mediaserver.LibraryIDsOf(changes) {
		if err := c.call(ctx, http.MethodPost, mediaserver.Join(c.base, "/api/v1/libraries/"+url.PathEscape(id)+"/scan"), nil); err != nil {
			return err
		}
	}
	return nil
}
