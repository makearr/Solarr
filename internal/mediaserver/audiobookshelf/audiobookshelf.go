// Package audiobookshelf is the client for an Audiobookshelf server, signed in with a user's API token.
package audiobookshelf

import (
	"context"
	"net/http"
	"net/url"
	"slices"

	"github.com/makearr/Solarr/internal/mediaserver"
)

// Client talks to one Audiobookshelf server.
type Client struct {
	doer  mediaserver.Doer
	base  string
	token string
	shelf shelf
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

type progress struct {
	LibraryItemID string  `json:"libraryItemId"`
	EpisodeID     string  `json:"episodeId"`
	Duration      float64 `json:"duration"`
	CurrentTime   float64 `json:"currentTime"`
	IsFinished    bool    `json:"isFinished"`
	LastUpdate    int64   `json:"lastUpdate"`
}

// Played lists a user's books by last progress, newest first; the files come from one item read each.
func (c *Client) Played(ctx context.Context, userID string, since int64, offset, limit int) ([]mediaserver.Play, bool, error) {
	var reply struct {
		MediaProgress []progress `json:"mediaProgress"`
	}
	if err := mediaserver.Call(ctx, c.doer, http.MethodGet, mediaserver.Join(c.base, "/api/users/"+url.PathEscape(userID)), c.header(), nil, &reply); err != nil {
		return nil, false, mediaserver.Refused(err)
	}
	books := slices.DeleteFunc(reply.MediaProgress, func(one progress) bool { return one.EpisodeID != "" })
	slices.SortFunc(books, func(a, b progress) int { return int(b.LastUpdate/1000 - a.LastUpdate/1000) })
	var plays []mediaserver.Play
	for position := offset; position < min(offset+limit, len(books)); position++ {
		one := books[position]
		if one.LastUpdate/1000 < since {
			return plays, true, nil
		}
		var item struct {
			Path  string `json:"path"`
			Media struct {
				Metadata struct {
					ASIN string `json:"asin"`
					ISBN string `json:"isbn"`
				} `json:"metadata"`
			} `json:"media"`
		}
		address := mediaserver.Join(c.base, "/api/items/"+url.PathEscape(one.LibraryItemID))
		if err := mediaserver.Call(ctx, c.doer, http.MethodGet, address, c.header(), nil, &item); err != nil {
			return nil, false, mediaserver.Refused(err)
		}
		ids := map[string]string{}
		if item.Media.Metadata.ASIN != "" {
			ids["asin"] = item.Media.Metadata.ASIN
		}
		if item.Media.Metadata.ISBN != "" {
			ids["isbn"] = item.Media.Metadata.ISBN
		}
		plays = append(plays, mediaserver.Play{
			Path: item.Path, ProviderIDs: ids, Whole: true, Played: one.IsFinished, PlayCount: boolCount(one.IsFinished),
			PositionMS: int64(one.CurrentTime * 1000), DurationMS: int64(one.Duration * 1000), LastPlayed: one.LastUpdate / 1000,
		})
	}
	return plays, offset+limit >= len(books), nil
}

func boolCount(finished bool) int64 {
	if finished {
		return 1
	}
	return 0
}
