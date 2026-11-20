package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// ErrInOtherCategory is a torrent the client already holds under a category that is not ours; it is the user's, so it is left alone.
var ErrInOtherCategory = errors.New("the client already has this torrent under another category")

// adoptTorrent looks for the torrent already in its client. Held in our category it is adopted (ref answered), so no
// second release is sent for the item; held under another category it is refused with that said; absent, ref is empty.
func adoptTorrent(ctx context.Context, client download.Client, file []byte, category string) (string, error) {
	by, ok := client.(download.ByReference)
	if !ok {
		return "", nil
	}
	hash, err := download.InfoHash(file)
	if err != nil {
		return "", nil //nolint:nilerr // no hash to look for: the add goes ahead and the client decides
	}
	found, err := by.DownloadsByRef(ctx, []string{hash})
	if err != nil || len(found) == 0 {
		return "", nil //nolint:nilerr // a failed look-up is not a reason to hold the release back
	}
	if found[0].Category != category {
		return "", fmt.Errorf("%w: %w (%q)", download.ErrRefused, ErrInOtherCategory, found[0].Category)
	}
	return found[0].ClientRef, nil
}
