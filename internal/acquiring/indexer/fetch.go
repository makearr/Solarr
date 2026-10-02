package indexer

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Fetch downloads the file a search result links to (an NZB), so the download client never has
// to reach the indexer itself -- Lidarr's way, and the indexer's own egress and rate limit apply.
func (c Client) Fetch(ctx context.Context, link string) ([]byte, error) {
	body, err := c.getBody(ctx, link)
	var magnet *httpx.MagnetRedirect
	if errors.As(err, &magnet) {
		return []byte(magnet.Link), nil // the client takes the link itself
	}
	if err != nil {
		return nil, fmt.Errorf("indexer: fetch release: %w", err)
	}
	if len(body) == 0 {
		return nil, errors.New("indexer: fetch release: empty response")
	}
	return body, nil
}
