// Package listenbrainz reads how often an album's release group was listened to on ListenBrainz (no key).
package listenbrainz

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

const (
	address          = "https://api.listenbrainz.org/1/popularity/release-group"
	maxResponseBytes = 1 << 20
)

// ErrNotFound means ListenBrainz holds no listens for the release group.
var ErrNotFound = errors.New("listenbrainz: no listens")

// Doer is the httpx client.
type Doer interface {
	Do(ctx context.Context, request *http.Request) (*http.Response, error)
}

// Client asks ListenBrainz.
type Client struct{ http Doer }

// New builds a client.
func New(client Doer) Client { return Client{http: client} }

// Popularity is a release group's total listens and the listeners behind them.
type Popularity struct{ Listens, Listeners int64 }

type wireRow struct {
	Listens   *int64 `json:"total_listen_count"`
	Listeners *int64 `json:"total_user_count"`
}

// ReleaseGroup answers the popularity of a MusicBrainz release group; ListenBrainz sends nulls for one it has not seen.
func (c Client) ReleaseGroup(ctx context.Context, mbid string) (Popularity, error) {
	body, err := json.Marshal(map[string][]string{"release_group_mbids": {mbid}})
	if err != nil {
		return Popularity{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Popularity{}, fmt.Errorf("listenbrainz: status %d", response.StatusCode)
	}
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: read: %w", err)
	}
	var rows []wireRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return Popularity{}, fmt.Errorf("listenbrainz: decode: %w", err)
	}
	if len(rows) == 0 || rows[0].Listens == nil || *rows[0].Listens <= 0 {
		return Popularity{}, ErrNotFound
	}
	found := Popularity{Listens: *rows[0].Listens}
	if rows[0].Listeners != nil {
		found.Listeners = *rows[0].Listeners
	}
	return found, nil
}
