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
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/hitcount"
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

// Cache fetches and stores pictures under Dir, which the cache budget evicts by age and size; a library item's
// picture is promoted to CoverDir, which the budget never touches.
type Cache struct {
	HTTP Doer
	FS   fsx.FS
	Dir  string
	// CoverDir holds the pictures of library items; empty keeps everything in Dir.
	CoverDir string
	// Hosts are the image hosts that may be fetched, each matching itself and its subdomains.
	Hosts []string
	// Lookups counts the pictures found already cached; optional.
	Lookups *hitcount.Counter
	// Clock dates a use of a cached picture, which Shrink goes by; without it uses are not recorded.
	Clock clock.Clock

	mu    sync.Mutex
	locks map[string]*imageLock
}

type imageLock struct {
	mu   sync.Mutex
	refs int
}

// DefaultHosts are the CDNs the TV, film, book and album providers' pictures live on.
var DefaultHosts = []string{
	"image.tmdb.org", "thetvdb.com", "tvmaze.com", "anilist.co", "fanart.tv", "coverartarchive.org", "wikimedia.org",
	"openlibrary.org", "books.google.com", "googleusercontent.com", "media-amazon.com", "mzstatic.com", "fantlab.ru", "hardcover.app", "stashdb.org", "fansdb.cc", "theporndb.net",
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
	if c.cached(key) || c.stored(key) {
		c.Lookups.Hit()
		c.renew(key)
		return key, nil
	}
	c.Lookups.Miss()
	release := c.lockFor(key)
	defer release()
	if c.cached(key) || c.stored(key) {
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

// usedRenewal is how stale a picture's date must be before a use renews it, so a busy wall costs one write a day.
const usedRenewal = 24 * time.Hour

// renew dates a cached picture's use as now; best effort, and only for the cache, which is what Shrink reads.
func (c *Cache) renew(key string) {
	if c.Clock == nil {
		return
	}
	info, err := c.FS.Stat(c.path(key, artwork.Full))
	now := c.Clock.Now()
	if err != nil || now.Sub(info.ModTime) < usedRenewal {
		return
	}
	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		_ = c.FS.Chtimes(c.path(key, size), now)
	}
}

// Promote makes the picture a library item's: it moves from the cache to CoverDir without asking the provider, and is
// fetched straight into CoverDir only when the cache never had it.
func (c *Cache) Promote(ctx context.Context, address string) (string, error) {
	key, err := c.Ensure(ctx, address)
	if err != nil || c.CoverDir == "" {
		return key, err
	}
	release := c.lockFor(key)
	defer release()
	if c.stored(key) || !c.cached(key) {
		return key, nil
	}
	if err := PromoteFiles(c.FS, c.Dir, c.CoverDir, key); err != nil {
		return "", fmt.Errorf("remoteimage: promote %s: %w", key, err)
	}
	return key, nil
}

// Stored says whether the picture at the address is already kept for the library.
func (c *Cache) Stored(address string) bool {
	if c.CoverDir == "" {
		return c.Cached(address)
	}
	return c.stored(Key(address))
}

func (c *Cache) stored(key string) bool {
	if c.CoverDir == "" {
		return false
	}
	_, err := c.FS.Stat(filepath.Join(c.CoverDir, FileName(key, artwork.Full)))
	return err == nil
}

// MoveLibraryCovers moves the covers of albums and people (work-N and person-N keys) from the cache directory to
// the library's, for an installation that kept them in the cache; it answers how many files moved.
func MoveLibraryCovers(fsys fsx.FS, from, to string) (int, error) {
	entries, err := fsys.ReadDir(from)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	moved := 0
	for _, entry := range entries {
		name := entry.Name
		if !strings.HasPrefix(name, "work-") && !strings.HasPrefix(name, "person-") {
			continue
		}
		if err := fsys.MkdirAll(to, 0o755); err != nil {
			return moved, err
		}
		if err := fsys.Rename(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// FileName is a picture's file name at a size.
func FileName(key string, size artwork.Size) string {
	name := map[artwork.Size]string{artwork.Thumb: "thumb", artwork.Small: "small", artwork.Full: "full"}[size]
	return key + "-" + name + ".webp"
}

// PromoteFiles moves a key's pictures, the full size last so a half-moved set reads as absent, from one directory to
// another; it copies when the two are different filesystems.
func PromoteFiles(fsys fsx.FS, from, to, key string) error {
	if err := fsys.MkdirAll(to, 0o755); err != nil {
		return err
	}
	for _, size := range []artwork.Size{artwork.Thumb, artwork.Small, artwork.Full} {
		source, destination := filepath.Join(from, FileName(key, size)), filepath.Join(to, FileName(key, size))
		if err := fsys.Rename(source, destination); err == nil {
			continue
		}
		file, err := fsys.Open(source)
		if err != nil {
			return err
		}
		err = fsx.WriteAtomic(fsys, destination, func(w io.Writer) error { _, err := io.Copy(w, file); return err })
		_ = file.Close()
		if err != nil {
			return err
		}
		if err := fsys.Remove(source); err != nil {
			return err
		}
	}
	return nil
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
	return filepath.Join(c.Dir, FileName(key, size))
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
