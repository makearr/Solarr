package comic

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// maxReadPage bounds one page image streamed to the reader; a larger entry is refused rather than held in memory.
const maxReadPage = 64 << 20

// ErrNoSuchPage is a page name the issue does not hold.
var ErrNoSuchPage = errors.New("comic: no such page")

// PageReader lists and reads the pages of issue files (cbz, cbr, cb7, cbt) for the in-browser reader. It keeps the page
// list of the few issues read last, since listing a RAR or 7z walks the whole archive.
type PageReader struct {
	FS fsx.FS

	mu    sync.Mutex
	lists map[string]pageList
}

type pageList struct {
	names    []string
	size     int64
	modified time.Time
}

// maxListed bounds the page lists kept.
const maxListed = 16

// PageNames lists the page images of an issue file in reading order: a page named cover first, the rest numbered naturally.
func (r *PageReader) PageNames(ctx context.Context, issue string) ([]string, error) {
	info, err := r.FS.Stat(issue)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	cached, found := r.lists[issue]
	r.mu.Unlock()
	if found && cached.size == info.Size && cached.modified.Equal(info.ModTime) {
		return slices.Clone(cached.names), nil
	}
	listed, _, err := archive.Peek(ctx, r.FS, issue, func(archive.Entry) int64 { return 0 }, maxCoverEntries)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range listed {
		if !entry.Entry.IsDir && !entry.Entry.Encrypted && isPage(path.Base(entry.Entry.Name)) {
			names = append(names, entry.Entry.Name)
		}
	}
	slices.SortStableFunc(names, func(a, b string) int { return pageOrder(path.Base(a), path.Base(b)) })
	r.mu.Lock()
	if r.lists == nil || len(r.lists) >= maxListed {
		r.lists = map[string]pageList{}
	}
	r.lists[issue] = pageList{names: names, size: info.Size, modified: info.ModTime}
	r.mu.Unlock()
	return slices.Clone(names), nil
}

// ReadPage is the bytes of one page of an issue file and its media type.
func (r *PageReader) ReadPage(ctx context.Context, issue, name string) (data []byte, contentType string, err error) {
	names, err := r.PageNames(ctx, issue)
	if err != nil {
		return nil, "", err
	}
	if !slices.Contains(names, name) {
		return nil, "", ErrNoSuchPage
	}
	read, _, err := archive.Peek(ctx, r.FS, issue, func(entry archive.Entry) int64 {
		if entry.Name == name && entry.SizeBytes <= maxReadPage {
			return entry.SizeBytes
		}
		return 0
	}, maxCoverEntries)
	if err != nil {
		return nil, "", err
	}
	for _, entry := range read {
		if entry.Entry.Name == name && len(entry.Head) > 0 {
			return entry.Head, pageContentType(name), nil
		}
	}
	return nil, "", fmt.Errorf("%w: %s is empty or over %d bytes", ErrNoSuchPage, name, maxReadPage)
}

// pageContentType is the media type a browser is told for a page image.
func pageContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".jxl":
		return "image/jxl"
	}
	return "application/octet-stream"
}
