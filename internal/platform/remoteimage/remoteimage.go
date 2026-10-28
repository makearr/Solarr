// Package remoteimage keeps provider pictures (posters, photos, logos) in Solarr's own image cache, fetched once when
// first asked for and resized like cover art, so the browser never talks to a provider's CDN.
package remoteimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ErrNotAllowed means the address is not one of the providers' image hosts.
var ErrNotAllowed = errors.New("remoteimage: not an allowed image address")

// ErrUnavailable means the provider did not hand over a usable picture.
var ErrUnavailable = errors.New("remoteimage: the picture could not be fetched")

// maxBytes bounds one fetched picture.
const maxBytes = 20 << 20

// Doer is the one method of httpx.Client this package needs.
type Doer interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// Cache fetches and stores pictures under Dir, in the same flat directory the cover art uses, so the cache budget
// evicts them by age and size like the rest.
type Cache struct {
	HTTP Doer
	FS   fsx.FS
	Dir  string
	// Hosts are the image hosts that may be fetched, each matching itself and its subdomains.
	Hosts []string

	mu    sync.Mutex
	locks map[string]*imageLock
}

type imageLock struct {
	mu   sync.Mutex
	refs int
}

// DefaultHosts are the CDNs the TV, film and book providers' pictures live on.
var DefaultHosts = []string{
	"image.tmdb.org", "thetvdb.com", "tvmaze.com", "anilist.co", "fanart.tv",
	"openlibrary.org", "books.google.com", "googleusercontent.com", "media-amazon.com",
}

// Key is the cache key an address is stored under; it is stable, so a repeat request finds the file.
func Key(address string) string {
	sum := sha256.Sum256([]byte(address))
	return "remote-" + hex.EncodeToString(sum[:12])
}

func (c *Cache) allowed(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range c.Hosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// Ensure makes sure the picture at the address is cached at every size and answers its cache key.
func (c *Cache) Ensure(ctx context.Context, address string) (string, error) {
	if !c.allowed(address) {
		return "", ErrNotAllowed
	}
	key := Key(address)
	if c.cached(key) {
		return key, nil
	}
	release := c.lockFor(key)
	defer release()
	if c.cached(key) {
		return key, nil
	}
	source, err := c.download(ctx, address)
	if err != nil {
		return "", err
	}
	sizes, err := artwork.Process(source)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err := c.FS.MkdirAll(c.Dir, 0o755); err != nil {
		return "", fmt.Errorf("remoteimage: create %s: %w", c.Dir, err)
	}
	// Full is written last: it is what cached() looks for, so a half-written set is fetched again.
	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		image := sizes[size]
		if err := fsx.WriteAtomic(c.FS, c.path(key, size), func(w io.Writer) error {
			_, err := w.Write(image.Bytes)
			return err
		}); err != nil {
			return "", fmt.Errorf("remoteimage: write: %w", err)
		}
	}
	return key, nil
}

// Original answers the picture at the address as the provider serves it, for a file a media server reads; nothing is
// cached, and only the providers' image hosts are asked.
func (c *Cache) Original(ctx context.Context, address string) ([]byte, error) {
	if !c.allowed(address) {
		return nil, ErrNotAllowed
	}
	return c.fetch(ctx, address)
}

func (c *Cache) path(key string, size artwork.Size) string {
	name := map[artwork.Size]string{artwork.Thumb: "thumb", artwork.Small: "small", artwork.Full: "full"}[size]
	return filepath.Join(c.Dir, key+"-"+name+".webp")
}

// Cached says whether the picture at the address is already stored at every size.
func (c *Cache) Cached(address string) bool { return c.cached(Key(address)) }

func (c *Cache) cached(key string) bool {
	_, err := c.FS.Stat(c.path(key, artwork.Full))
	return err == nil
}

func (c *Cache) lockFor(key string) func() {
	c.mu.Lock()
	if c.locks == nil {
		c.locks = map[string]*imageLock{}
	}
	lock := c.locks[key]
	if lock == nil {
		lock = &imageLock{}
		c.locks[key] = lock
	}
	lock.refs++
	c.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		c.mu.Lock()
		lock.refs--
		if lock.refs == 0 && c.locks[key] == lock {
			delete(c.locks, key)
		}
		c.mu.Unlock()
	}
}

// tmdbOriginal is the path TMDB serves a picture in its full size under; its 500-pixel rendition is all a wall needs.
const tmdbOriginal = "/t/p/original/"

func (c *Cache) download(ctx context.Context, address string) ([]byte, error) {
	if strings.HasPrefix(address, "https://image.tmdb.org"+tmdbOriginal) {
		smaller := strings.Replace(address, tmdbOriginal, "/t/p/w500/", 1)
		if source, err := c.fetch(ctx, smaller); err == nil {
			return source, nil
		}
	}
	return c.fetch(ctx, address)
}

func (c *Cache) fetch(ctx context.Context, address string) ([]byte, error) {
	response, err := c.HTTP.Get(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	source, err := httpx.ReadLimited(response.Body, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return source, nil
}
