package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/qbittorrent"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// IndexerClientFactory builds a Newznab/Torznab client for one connection's
// own base URL, decrypted API key and egress settings -- injected so this
// package never builds an httpx.Client itself (cmd/solarr/app/
// providers.go's own job, the same rate limiter/breaker/proxy wiring every
// other provider already gets), the same split LidarrClientFactory already
// uses.
type IndexerClientFactory func(indexerID int64, baseURL, apiKey string, egress settings.IndexerEgress) indexer.Client

// DownloaderHTTPFactory builds the HTTP client of one download connection: its own rate-limit and breaker bucket,
// the SSRF guard for the configured host.
type DownloaderHTTPFactory func(downloaderID int64, baseURL string) *httpx.Client

// ConnectionTester runs todo 5.2's own "every connection tested before it is
// accepted": a read-only capability/version fetch against the real indexer
// or download client, never a search or dispatch action.
type ConnectionTester struct {
	Cache      *settings.Cache
	Indexer    IndexerClientFactory
	Downloader DownloaderHTTPFactory
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
		return apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}, nil //nolint:nilerr
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

	return c.testDownloader(ctx, found.Impl, c.Downloader(id, found.BaseURL), found.BaseURL, credentials.Reveal(), found.Options()), nil
}

// TestDownloaderConfig tests a client before it is saved: the form's "Test" before "Add".
func (c ConnectionTester) TestDownloaderConfig(
	ctx context.Context, impl, baseURL, credentials string, options settings.DownloaderOptions,
) (apiserver.ConnectionTestResult, error) {
	return c.testDownloader(ctx, impl, c.Downloader(0, baseURL), baseURL, credentials, options), nil
}

func (c ConnectionTester) testDownloader(
	ctx context.Context, impl string, client *httpx.Client, baseURL, credentials string, options settings.DownloaderOptions,
) apiserver.ConnectionTestResult {
	switch impl {
	case ImplSABnzbd:
		return c.testSABnzbd(ctx, sabnzbd.NewClient(client, baseURL, credentials), options)
	case ImplQBittorrent:
		return c.testQBittorrent(ctx, qbittorrent.NewClient(client, baseURL, credentials), options)
	}
	return apiserver.ConnectionTestResult{Message: fmt.Sprintf("%q is not a download client Solarr knows", impl), TestedAt: c.Clock.Now()}
}

// testQBittorrent proves the address and the login (the version call needs it), and that the categories downloads are
// sent under exist, since qBittorrent files an unknown one under no category at all.
func (c ConnectionTester) testQBittorrent(ctx context.Context, client qbittorrent.Client, options settings.DownloaderOptions) apiserver.ConnectionTestResult {
	at := c.Clock.Now()
	version, err := client.Version(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}
	}
	message := fmt.Sprintf("Connected. qBittorrent %s.", version)
	if categories, err := client.Categories(ctx); err == nil {
		for _, category := range c.sendCategories(options) {
			if !slices.Contains(categories, category) {
				message += fmt.Sprintf(" qBittorrent has no %q category yet: it is created when the first torrent is sent.", category)
			}
		}
	}
	folder, _ := client.SavePath(ctx)
	if folder != "" {
		message += fmt.Sprintf(" Finished downloads go to %s.", folder)
	}
	return apiserver.ConnectionTestResult{OK: true, Message: message, TestedAt: at, DownloadFolder: folder}
}

// testSABnzbd proves the address (version), the key (the download folder needs it) and that the
// categories downloads are sent under exist; a missing one would land everything in SABnzbd's "*".
func (c ConnectionTester) testSABnzbd(ctx context.Context, client sabnzbd.Client, options settings.DownloaderOptions) apiserver.ConnectionTestResult {
	at := c.Clock.Now()
	version, err := client.Version(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}
	}
	folder, err := client.CompleteFolder(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}
	}
	message := fmt.Sprintf("Connected. Version %s. Finished downloads go to %s.", version, folder)
	if categories, err := client.Categories(ctx); err == nil {
		for _, category := range c.sendCategories(options) {
			if !slices.Contains(categories, category) {
				message += fmt.Sprintf(" SABnzbd has no %q category yet: add it there, or downloads land in its default.", category)
			}
		}
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

// sendCategories lists the client categories downloads are sent under: one per media type that has a library.
func (c ConnectionTester) sendCategories(options settings.DownloaderOptions) []string {
	var mediaTypes []string
	for _, category := range c.Cache.Snapshot().Categories() {
		mediaTypes = append(mediaTypes, category.MediaType)
	}
	return ClientCategories(options, mediaTypes)
}

// ClientCategories is each distinct client category the given media types are sent under, in their order.
func ClientCategories(options settings.DownloaderOptions, mediaTypes []string) []string {
	var categories []string
	for _, mediaType := range mediaTypes {
		if category := options.CategoryFor(mediaType); !slices.Contains(categories, category) {
			categories = append(categories, category)
		}
	}
	return categories
}
