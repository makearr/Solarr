// Package stash is the client for a Stash server over its GraphQL endpoint, signed in with an API key or with a user name and password.
package stash

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Stash server.
type Client struct {
	doer mediaserver.Doer
	base string
	key  string
	// session is set when the connection signs in with a user name and password; the key is then empty.
	session *mediaserver.Session
}

// NewLogin builds a client that signs in on Stash's login form and sends the session cookie it answers.
func NewLogin(doer mediaserver.Doer, base, username, password string) *Client {
	client := &Client{doer: doer, base: base}
	client.session = mediaserver.NewSession(func(ctx context.Context) (string, error) {
		return client.signIn(ctx, username, password)
	})
	return client
}

func (c *Client) signIn(ctx context.Context, username, password string) (string, error) {
	form := url.Values{"username": {username}, "password": {password}, "returnURL": {"/"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, mediaserver.Join(c.base, "/login"), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("stash: build login: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.doer.Do(ctx, request)
	if err != nil {
		return "", mediaserver.Refused(err)
	}
	defer func() { _ = response.Body.Close() }()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" && cookie.Value != "" {
			return cookie.Name + "=" + cookie.Value, nil
		}
	}
	return "", fmt.Errorf("%w: the server sent no session", mediaserver.ErrRejected)
}

// New builds a client for the server at base; an empty key is sent as none, for a server without authentication.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

func (c *Client) query(ctx context.Context, query string, into any) error {
	return c.run(ctx, map[string]any{"query": query}, into)
}

func (c *Client) run(ctx context.Context, body map[string]any, into any) error {
	header := func(cookie string) http.Header {
		if c.session != nil {
			return http.Header{"Cookie": {cookie}}
		}
		if c.key != "" {
			return http.Header{"ApiKey": {c.key}}
		}
		return http.Header{}
	}
	err := mediaserver.CallSigned(ctx, c.doer, c.session, header, http.MethodPost, mediaserver.Join(c.base, "/graphql"), body, into)
	return mediaserver.Refused(err)
}

// Test reads the version; a Stash key has no administrator notion.
func (c *Client) Test(ctx context.Context) (mediaserver.Info, error) {
	var reply struct {
		Data struct {
			Version struct {
				Version string `json:"version"`
			} `json:"version"`
		} `json:"data"`
	}
	if err := c.query(ctx, "query { version { version } }", &reply); err != nil {
		return mediaserver.Info{}, err
	}
	return mediaserver.Info{Name: "Stash", Version: reply.Data.Version.Version, Admin: true}, nil
}

// Libraries answers Stash's one library, with the folders it scans.
func (c *Client) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	var reply struct {
		Data struct {
			Configuration struct {
				General struct {
					Stashes []struct {
						Path string `json:"path"`
					} `json:"stashes"`
				} `json:"general"`
			} `json:"configuration"`
		} `json:"data"`
	}
	if err := c.query(ctx, "query { configuration { general { stashes { path } } } }", &reply); err != nil {
		return nil, err
	}
	var paths []string
	for _, stash := range reply.Data.Configuration.General.Stashes {
		paths = append(paths, stash.Path)
	}
	return []mediaserver.Library{{ID: "stash", Name: "Stash", Type: "adult", Paths: paths}}, nil
}

// maxTargetedFolders is how many folders of one batch are scanned one by one before the whole library is scanned instead.
const maxTargetedFolders = 50

// Changed starts Stash's metadata scan on the folders the new files are in, or on every library folder when there are many.
func (c *Client) Changed(ctx context.Context, changes []mediaserver.Change) error {
	var folders []string
	for _, change := range changes {
		if change.Kind == "deleted" {
			continue
		}
		if folder := path.Dir(change.Path); !slices.Contains(folders, folder) {
			folders = append(folders, folder)
		}
	}
	if len(folders) == 0 {
		return nil
	}
	input := map[string]any{}
	if len(folders) <= maxTargetedFolders {
		input["paths"] = folders
	}
	body := map[string]any{"query": "mutation($input: ScanMetadataInput!) { metadataScan(input: $input) }", "variables": map[string]any{"input": input}}
	return c.run(ctx, body, nil)
}
