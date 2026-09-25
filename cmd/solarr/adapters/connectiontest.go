package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// IndexerClientFactory builds a Newznab/Torznab client for one connection's
// own base URL, decrypted API key and egress settings -- injected so this
// package never builds an httpx.Client itself (cmd/solarr/app/
// providers.go's own job, the same rate limiter/breaker/proxy wiring every
// other provider already gets), the same split LidarrClientFactory already
// uses.
type IndexerClientFactory func(indexerID int64, baseURL, apiKey string, egress settings.IndexerEgress) indexer.Client

// DownloaderClientFactory builds a download client for one connection's own
// base URL and decrypted credentials. Only SABnzbd exists today (ADR-46).
type DownloaderClientFactory func(downloaderID int64, baseURL, credentials string) sabnzbd.Client

// ConnectionTester runs todo 5.2's own "every connection tested before it is
// accepted": a read-only capability/version fetch against the real indexer
// or download client, never a search or dispatch action.
type ConnectionTester struct {
	Cache      *settings.Cache
	Indexer    IndexerClientFactory
	Downloader DownloaderClientFactory
	Clock      clock.Clock
}

// TestIndexer fetches t=caps and records the response the same way a
// periodic caps refresh would (ADR-43) -- a connection test earns the same
// side effect a dedicated refresh action would have had anyway.
func (c ConnectionTester) TestIndexer(ctx context.Context, id int64) (apiserver.ConnectionTestResult, error) {
	found, err := c.Cache.GetIndexer(ctx, id)
	if err != nil {
		return apiserver.ConnectionTestResult{}, err
	}
	apiKey, err := c.Cache.DecryptedIndexerAPIKey(ctx, id)
	if err != nil {
		return apiserver.ConnectionTestResult{}, err
	}

	at := c.Clock.Now()
	client := c.Indexer(id, found.BaseURL, apiKey.Reveal(), found.Egress())
	caps, err := client.Capabilities(ctx)
	if err != nil {
		// A connection problem is the answer this method exists to report,
		// not a failure of the method call itself -- ok:false, never an error.
		return apiserver.ConnectionTestResult{Message: err.Error(), TestedAt: at}, nil //nolint:nilerr
	}

	if encoded, err := json.Marshal(caps); err == nil {
		// Best-effort: a failed write must not turn a successful connection
		// test into a reported failure -- the caps response itself is what
		// answers the request either way.
		_ = c.Cache.SetIndexerCaps(ctx, id, string(encoded), at.Unix())
	}
	return apiserver.ConnectionTestResult{
		OK:       true,
		Message:  fmt.Sprintf("Connected. %d categories, %d search modes.", len(caps.Categories), countAvailable(caps.Search)),
		TestedAt: at,
	}, nil
}

// TestDownloader fetches the client's own reported version -- SABnzbd's own
// connection-test role for Version(), never a queue or dispatch action.
func (c ConnectionTester) TestDownloader(ctx context.Context, id int64) (apiserver.ConnectionTestResult, error) {
	found, err := c.Cache.GetDownloader(ctx, id)
	if err != nil {
		return apiserver.ConnectionTestResult{}, err
	}
	credentials, err := c.Cache.DecryptedDownloaderCredentials(ctx, id)
	if err != nil {
		return apiserver.ConnectionTestResult{}, err
	}

	return c.testSABnzbd(ctx, c.Downloader(id, found.BaseURL, credentials.Reveal()), found.Options()), nil
}

// TestDownloaderConfig tests a client before it is saved: the form's "Test" before "Add".
func (c ConnectionTester) TestDownloaderConfig(
	ctx context.Context, impl, baseURL, credentials string, options settings.DownloaderOptions,
) (apiserver.ConnectionTestResult, error) {
	if impl != "sabnzbd" {
		return apiserver.ConnectionTestResult{Message: fmt.Sprintf("%q is not a download client Solarr knows", impl), TestedAt: c.Clock.Now()}, nil
	}
	return c.testSABnzbd(ctx, c.Downloader(0, baseURL, credentials), options), nil
}

// testSABnzbd proves the address (version), the key (the download folder needs it) and that the
// category music is sent under exists; a missing one would land everything in SABnzbd's "*".
func (c ConnectionTester) testSABnzbd(ctx context.Context, client sabnzbd.Client, options settings.DownloaderOptions) apiserver.ConnectionTestResult {
	at := c.Clock.Now()
	version, err := client.Version(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: err.Error(), TestedAt: at}
	}
	folder, err := client.CompleteFolder(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: err.Error(), TestedAt: at}
	}
	message := fmt.Sprintf("Connected. Version %s. Finished downloads go to %s.", version, folder)
	category := options.CategoryFor("music")
	if categories, err := client.Categories(ctx); err == nil && !slices.Contains(categories, category) {
		message += fmt.Sprintf(" SABnzbd has no %q category yet: add it there, or downloads land in its default.", category)
	}
	return apiserver.ConnectionTestResult{OK: true, Message: message, TestedAt: at, DownloadFolder: folder}
}

// countAvailable counts how many of a caps response's own search modes are
// actually available -- the raw map always carries all six keys regardless.
func countAvailable(search map[string]indexer.SearchFunction) int {
	count := 0
	for _, function := range search {
		if function.Available {
			count++
		}
	}
	return count
}
