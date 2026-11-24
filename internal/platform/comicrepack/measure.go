package comicrepack

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"  // registers the GIF decoder for DecodeConfig
	_ "image/jpeg" // registers the JPEG decoder for DecodeConfig
	_ "image/png"  // registers the PNG decoder for DecodeConfig
	"path"
	"slices"
	"strings"

	_ "golang.org/x/image/webp" // registers the WebP decoder for DecodeConfig

	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Bounds of a measurement: how much of an archive is read to learn its pages.
const (
	maxEntries  = 20000
	headerBytes = 64 << 10
	fullHeaders = 200
	sampleEvery = 8
	// spreadRatio is how much wider than tall a page must be, in a book of portrait pages, to count as a double-page spread.
	spreadRatio = 1.2
)

// Pages are the facts of an archive's page images, read from their headers.
type Pages struct {
	Count int
	// MedianLong and MedianShort are the median edges of the pages that are not spreads; 0 when none could be read.
	MedianLong, MedianShort int
	// Format is the commonest page format by extension ("jpg", "png", "webp"...).
	Format string
}

var measuredFormats = map[string]string{".jpg": "jpg", ".jpeg": "jpg", ".png": "png", ".gif": "gif", ".webp": "webp", ".avif": "avif", ".jxl": "jxl"}

// Measure reads a comic archive's page facts without extracting it.
func Measure(ctx context.Context, fsys fsx.FS, source string) (Pages, error) {
	seen := 0
	want := func(entry archive.Entry) int64 {
		if _, isImage := measuredFormats[strings.ToLower(path.Ext(entry.Name))]; !isImage {
			return 0
		}
		seen++
		if seen > fullHeaders && seen%sampleEvery != 0 {
			return 0
		}
		return headerBytes
	}
	entries, _, err := archive.Peek(ctx, fsys, source, want, maxEntries)
	if err != nil {
		return Pages{}, err
	}
	var pages Pages
	formats := map[string]int{}
	type size struct{ width, height int }
	var sizes []size
	for _, each := range entries {
		format, isImage := measuredFormats[strings.ToLower(path.Ext(each.Entry.Name))]
		if !isImage {
			continue
		}
		pages.Count++
		formats[format]++
		if each.Head == nil {
			continue
		}
		if config, _, err := image.DecodeConfig(bytes.NewReader(each.Head)); err == nil && config.Width > 0 && config.Height > 0 {
			sizes = append(sizes, size{config.Width, config.Height})
		}
	}
	pages.Format = commonest(formats)
	portrait := 0
	for _, each := range sizes {
		if each.height > each.width {
			portrait++
		}
	}
	var longs, shorts []int
	for _, each := range sizes {
		if portrait*2 > len(sizes) && float64(each.width) > spreadRatio*float64(each.height) {
			continue
		}
		longs, shorts = append(longs, max(each.width, each.height)), append(shorts, min(each.width, each.height))
	}
	if len(longs) > 0 {
		slices.Sort(longs)
		slices.Sort(shorts)
		pages.MedianLong, pages.MedianShort = longs[len(longs)/2], shorts[len(shorts)/2]
	}
	return pages, nil
}

// commonest is the key with the highest count, the alphabetically first on a tie.
func commonest(counts map[string]int) string {
	best, bestCount := "", 0
	for key, n := range counts {
		if n > bestCount || n == bestCount && key < best {
			best, bestCount = key, n
		}
	}
	return best
}
