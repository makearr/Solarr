package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// IndexerHTTPFactory builds the HTTP client of one indexer connection: its own rate-limit and breaker bucket, the SSRF
// guard for its host and its egress proxy -- injected so this package never builds an httpx.Client itself.
type IndexerHTTPFactory func(indexerID int64, baseURL string, egress settings.IndexerEgress) *httpx.Client

// DownloaderHTTPFactory builds the HTTP client of one download connection: its own rate-limit and breaker bucket,
// the SSRF guard for the configured host.
type DownloaderHTTPFactory func(downloaderID int64, baseURL string) *httpx.Client

// ConnectionTester runs todo 5.2's own "every connection tested before it is
// accepted": a read-only capability/version fetch against the real indexer
// or download client, never a search or dispatch action.
type ConnectionTester struct {
	Cache      *settings.Cache
	Indexer    IndexerHTTPFactory
	Downloader DownloaderHTTPFactory
	// FS is where a blackhole's folders are looked for.
	FS    fsx.FS
	Clock clock.Clock
	// SharedCategories answers the categories connected *Arrs send under on a client, with each *Arr's name; nil
	// skips the check.
	SharedCategories func(ctx context.Context, baseURL string) map[string]string
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

	result := c.testIndexer(ctx, found.Impl, c.Indexer(id, found.BaseURL, found.Egress()), found.BaseURL, apiKey.Reveal())
	if result.OK {
		// Best-effort: a failed write must not turn a successful connection
		// test into a reported failure -- the caps response itself is what
		// answers the request either way.
		_ = c.Cache.SetIndexerCaps(ctx, id, result.capsJSON, result.TestedAt.Unix())
	}
	return result.ConnectionTestResult, nil
}

// TestIndexerConfig tests an indexer before it is saved: the add page's "Test" before "Add".
func (c ConnectionTester) TestIndexerConfig(ctx context.Context, impl, baseURL, apiKey string) (apiserver.ConnectionTestResult, error) {
	return c.testIndexer(ctx, impl, c.Indexer(0, baseURL, settings.IndexerEgress{}), baseURL, apiKey).ConnectionTestResult, nil
}

type indexerTest struct {
	apiserver.ConnectionTestResult
	capsJSON string
}

// testIndexer asks any implementation for its capabilities: Newznab's t=caps, or an API indexer's cheapest
// authenticated call.
func (c ConnectionTester) testIndexer(ctx context.Context, impl string, client *httpx.Client, baseURL, apiKey string) indexerTest {
	at := c.Clock.Now()
	driver, known := NewIndexer(impl, IndexerDeps{HTTP: client, Clock: c.Clock}, indexer.Connection{BaseURL: baseURL, APIKey: apiKey})
	if !known {
		return indexerTest{ConnectionTestResult: apiserver.ConnectionTestResult{
			Message: fmt.Sprintf("%q is not an indexer Solarr knows", impl), TestedAt: at,
		}}
	}
	caps, err := driver.Capabilities(ctx)
	if err != nil {
		// A connection problem is the answer this method exists to report, not a failure of the call itself.
		return indexerTest{ConnectionTestResult: apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}}
	}
	encoded, _ := json.Marshal(caps)
	return indexerTest{
		ConnectionTestResult: apiserver.ConnectionTestResult{
			OK:       true,
			Message:  fmt.Sprintf("Connected. %d categories, %d search modes.", len(caps.Categories), countAvailable(caps.Search)),
			TestedAt: at,
		},
		capsJSON: string(encoded),
	}
}

// TestDownloader runs the client's own probe: its version, and what else it can say without changing anything.
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

// testDownloader probes any kind the same way, and checks the categories downloads are sent under: a client that
// does not create a missing one would file them under its default.
func (c ConnectionTester) testDownloader(
	ctx context.Context, impl string, client *httpx.Client, baseURL, credentials string, options settings.DownloaderOptions,
) apiserver.ConnectionTestResult {
	at := c.Clock.Now()
	definition, known := DownloadClientDefinition(impl)
	if !known {
		return apiserver.ConnectionTestResult{Message: fmt.Sprintf("%q is not a download client Solarr knows", impl), TestedAt: at}
	}
	driver, _ := NewDownloadClient(impl, DownloadClientDeps{HTTP: client, FS: c.FS, Clock: c.Clock},
		DownloadConnection(baseURL, credentials, options))
	probe, err := driver.Probe(ctx)
	if err != nil {
		return apiserver.ConnectionTestResult{Message: httpx.Explain(err), TestedAt: at}
	}
	message := fmt.Sprintf("Connected. %s.", probe.Version)
	if probe.DownloadFolder != "" {
		message += fmt.Sprintf(" Finished downloads go to %s.", probe.DownloadFolder)
	}
	if definition.Categories && probe.Categories != nil {
		for _, category := range c.sendCategories(options) {
			if slices.ContainsFunc(probe.Categories, func(have string) bool { return strings.EqualFold(have, category) }) {
				continue
			}
			if probe.CreatesCategories {
				message += fmt.Sprintf(" %s has no %q category yet: it is created when the first download is sent.", definition.Label, category)
			} else {
				message += fmt.Sprintf(" %s has no %q category yet: add it there, or downloads land in its default.", definition.Label, category)
			}
		}
	}
	for _, note := range probe.Notes {
		message += " " + note
	}
	result := apiserver.ConnectionTestResult{OK: true, Message: message, TestedAt: at, DownloadFolder: probe.DownloadFolder}
	if !definition.Categories || !definition.HasAddress() {
		return result
	}
	return c.warnShared(ctx, baseURL, options, result)
}

// warnShared fails a connected client whose send category a connected *Arr also uses: that *Arr would import
// Solarr's downloads into its own library before Solarr sees them.
func (c ConnectionTester) warnShared(
	ctx context.Context, baseURL string, options settings.DownloaderOptions, result apiserver.ConnectionTestResult,
) apiserver.ConnectionTestResult {
	if !result.OK || c.SharedCategories == nil {
		return result
	}
	shared := c.SharedCategories(ctx, baseURL)
	for _, category := range c.sendCategories(options) {
		if name, found := shared[strings.ToLower(category)]; found {
			result.OK = false
			result.Message += fmt.Sprintf(" %s also downloads into the %q category on this client and would import what Solarr sends"+
				" there: give Solarr a category of its own.", name, category)
		}
	}
	return result
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
