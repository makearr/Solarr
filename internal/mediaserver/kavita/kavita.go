// Package kavita is the client for a Kavita server, signed in with an auth key sent as x-api-key or with a user name and password.
package kavita

import (
	"context"
	"errors"
	"fmt"
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
	// session is set when the connection signs in with a user name and password; the key is then empty.
	session *mediaserver.Session
}

// New builds a client for the server at base with the auth key.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

// NewLogin builds a client that signs in as username and sends the token Kavita answers.
func NewLogin(doer mediaserver.Doer, base, username, password string) *Client {
	client := &Client{doer: doer, base: base}
	client.session = mediaserver.NewSession(func(ctx context.Context) (string, error) {
		return client.signIn(ctx, username, password)
	})
	return client
}

func (c *Client) signIn(ctx context.Context, username, password string) (string, error) {
	var reply struct {
		Token string `json:"token"`
	}
	body := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{username, password}
	if err := mediaserver.Call(ctx, c.doer, http.MethodPost, mediaserver.Join(c.base, "/api/Account/login"), nil, body, &reply); err != nil {
		return "", mediaserver.Refused(err)
	}
	if reply.Token == "" {
		return "", fmt.Errorf("%w: the server sent no token", mediaserver.ErrRejected)
	}
	return reply.Token, nil
}

func (c *Client) header(token string) http.Header {
	if c.session == nil {
		return http.Header{"X-Api-Key": {c.key}}
	}
	return http.Header{"Authorization": {"Bearer " + token}}
}

func (c *Client) call(ctx context.Context, method, address string, body, into any) error {
	return mediaserver.CallSigned(ctx, c.doer, c.session, c.header, method, address, body, into)
}

// Test asks for the libraries to prove the key, then for the user list, which only an administrator may read.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	if _, err := c.Libraries(ctx); err != nil {
		return mediaserver.Info{}, err
	}
	info := mediaserver.Info{Name: "Kavita"}
	var server struct {
		KavitaVersion string `json:"kavitaVersion"`
	}
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/Server/server-info-slim"), nil, &server); err == nil {
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
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/Library/libraries"), nil, &reply); err != nil {
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
	if err := c.call(ctx, http.MethodGet, mediaserver.Join(c.base, "/api/Users"), nil, &reply); err != nil {
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
		if err := c.call(ctx, http.MethodPost, address, nil, nil); err != nil {
			return err
		}
	}
	return nil
}
