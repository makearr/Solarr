// Package stash is the client for a Stash server over its GraphQL endpoint, signed in with an API key.
package stash

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Stash server.
type Client struct {
	doer mediaserver.Doer
	base string
	key  string
}

// New builds a client for the server at base; an empty key is sent as none, for a server without authentication.
func New(doer mediaserver.Doer, base, key string) *Client {
	return &Client{doer: doer, base: base, key: key}
}

func (c *Client) query(ctx context.Context, query string, into any) error {
	header := http.Header{}
	if c.key != "" {
		header.Set("ApiKey", c.key)
	}
	err := mediaserver.Call(ctx, c.doer, http.MethodPost, mediaserver.Join(c.base, "/graphql"), header, map[string]string{"query": query}, into)
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
