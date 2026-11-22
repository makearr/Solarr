// Package imdbdata reads IMDb's daily ratings dataset: one gzip download, parsed as it arrives and kept in metadata.db,
// so a rating costs a local read and no quota. IMDb allows the files for personal, non-commercial use.
package imdbdata

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/metacache"
)

const (
	datasetURL = "https://datasets.imdbws.com/title.ratings.tsv.gz"
	// chunkRows keeps each write transaction short (ADR-4).
	chunkRows = 200
	// maxLineBytes is far above a real line; a longer one means the file is not the dataset.
	maxLineBytes = 1 << 10
)

// Doer is the httpx client the download goes through.
type Doer interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// Store is the part of metacache.Cache this package needs.
type Store interface {
	SetImdbRatings(ctx context.Context, ratings map[string]metacache.ImdbRating) error
	ImdbRatingOf(ctx context.Context, title string) (metacache.ImdbRating, error)
	ImdbRatingCount(ctx context.Context) (int64, error)
}

// Client downloads the dataset and answers ratings from what it stored.
type Client struct {
	http  Doer
	store Store
}

// New builds a client over an httpx client and the metadata store.
func New(client Doer, store Store) Client { return Client{http: client, store: store} }

// ErrNotFound says the dataset holds no rating for the title, or was never downloaded.
var ErrNotFound = errors.New("imdbdata: no rating")

// Rating is the stored rating of an IMDb title (tt id).
func (c Client) Rating(ctx context.Context, title string) (score float64, votes int64, err error) {
	found, err := c.store.ImdbRatingOf(ctx, title)
	if errors.Is(err, metacache.ErrNotFound) {
		return 0, 0, ErrNotFound
	}
	return found.Rating, found.Votes, err
}

// Loaded says the dataset was downloaded at least once.
func (c Client) Loaded(ctx context.Context) bool {
	count, err := c.store.ImdbRatingCount(ctx)
	return err == nil && count > 0
}

// Sync downloads the dataset and stores every row; it answers how many titles were stored.
func (c Client) Sync(ctx context.Context) (int, error) {
	response, err := c.http.Get(ctx, datasetURL)
	if err != nil {
		return 0, fmt.Errorf("imdbdata: download: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("imdbdata: download: status %d", response.StatusCode)
	}
	return c.storeFrom(ctx, response.Body)
}

// storeFrom reads the gzip TSV from body in chunks of chunkRows.
func (c Client) storeFrom(ctx context.Context, body io.Reader) (int, error) {
	unzipped, err := gzip.NewReader(body)
	if err != nil {
		return 0, fmt.Errorf("imdbdata: gzip: %w", err)
	}
	defer func() { _ = unzipped.Close() }()
	scanner := bufio.NewScanner(unzipped)
	scanner.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	chunk := make(map[string]metacache.ImdbRating, chunkRows)
	stored := 0
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if err := c.store.SetImdbRatings(ctx, chunk); err != nil {
			return err
		}
		stored += len(chunk)
		chunk = make(map[string]metacache.ImdbRating, chunkRows)
		return nil
	}
	for scanner.Scan() {
		title, rating, ok := parseLine(scanner.Text())
		if !ok {
			continue
		}
		chunk[title] = rating
		if len(chunk) >= chunkRows {
			if err := flush(); err != nil {
				return stored, err
			}
			if err := ctx.Err(); err != nil {
				return stored, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stored, fmt.Errorf("imdbdata: read: %w", err)
	}
	return stored, flush()
}

// parseLine reads "tt0000001<TAB>5.7<TAB>2100"; the header line and anything malformed answer false.
func parseLine(line string) (string, metacache.ImdbRating, bool) {
	fields := strings.Split(line, "\t")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "tt") {
		return "", metacache.ImdbRating{}, false
	}
	score, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || score <= 0 || score > 10 {
		return "", metacache.ImdbRating{}, false
	}
	votes, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || votes < 0 {
		return "", metacache.ImdbRating{}, false
	}
	return fields[0], metacache.ImdbRating{Rating: score, Votes: votes}, true
}
