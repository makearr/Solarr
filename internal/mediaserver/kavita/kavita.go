// Package kavita is the client for a Kavita server, signed in with an auth key sent as x-api-key.
package kavita

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Kavita server.
type Client struct {
	doer mediaserver.Doer
	base string
	key  string
}

// New builds a client for the server at base with the auth key.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

func (c *Client) header() http.Header { return http.Header{"X-Api-Key": {c.key}} }

// Test asks for the libraries to prove the key, then for the user list, which only an administrator may read.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	if _, err := c.Libraries(ctx); err != nil {
		return mediaserver.Info{}, err
	}
	info := mediaserver.Info{Name: "Kavita"}
	var server struct {
		KavitaVersion string `json:"kavitaVersion"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/Server/server-info-slim"), c.header(), nil, &server); err == nil {
		info.Version = server.KavitaVersion
	}
	_, err := c.Users(ctx)
	switch {
	case err == nil:
		info.Admin = true
	case errors.Is(err, mediaserver.ErrNotAdmin):
	default:
		return info, err
	}
	return info, nil
}

// Libraries lists the libraries with their folders.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var reply []struct {
		ID      int      `json:"id"`
		Name    string   `json:"name"`
		Type    int      `json:"type"`
		Folders []string `json:"folders"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/Library/libraries"), c.header(), nil, &reply); err != nil {
		return nil, mediaserver.Refused(err)
	}
	libraries := make([]mediaserver.Library, len(reply))
	for index, library := range reply {
		libraries[index] = mediaserver.Library{ID: strconv.Itoa(library.ID), Name: library.Name, Type: libraryType(library.Type), Paths: library.Folders}
	}
	return libraries, nil
}

// libraryType names Kavita's numeric library type: 0 manga, 1 comic, 2 book, 3 image, 4 light novel, 5 comic (ComicVine).
func libraryType(kind int) string {
	switch kind {
	case 0:
		return "manga"
	case 1, 5:
		return "comic"
	case 2, 4:
		return "book"
	default:
		return "image"
	}
}

// Users lists the accounts; only an administrator's key may.
func (c *Client) Users(ctx context.Context) ([]mediaserver.User, error) {
	var reply []struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/Users"), c.header(), nil, &reply); err != nil {
		return nil, mediaserver.Refused(err)
	}
	users := make([]mediaserver.User, len(reply))
	for index, user := range reply {
		users[index] = mediaserver.User{ID: strconv.Itoa(user.ID), Name: user.Username}
	}
	return users, nil
}

// Changed scans each library the changes belong to once.
func (c *Client) Changed(ctx context.Context, changes []mediaserver.Change) error {
	for _, id := range mediaserver.LibraryIDsOf(changes) {
		address := mediaserver.Join(c.base, "/api/Library/scan") + mediaserver.Query(url.Values{"libraryId": {id}})
		if err := mediaserver.Call(ctx, c.doer, http.MethodPost, address, c.header(), nil, nil); err != nil {
			return err
		}
	}
	return nil
}
