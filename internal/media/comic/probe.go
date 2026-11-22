// Package comic is the comic media type: series of issues or volumes, read from cbz, cbr, cb7, cbt, pdf and epub files.
package comic

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"  // registers the GIF decoder for DecodeConfig
	_ "image/jpeg" // registers the JPEG decoder for DecodeConfig
	_ "image/png"  // registers the PNG decoder for DecodeConfig
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // registers the WebP decoder for DecodeConfig

	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Probe bounds: how much of an archive is read to learn its page facts.
const (
	maxProbeEntries   = 20000
	fullHeaderEntries = 2000
	sampleEvery       = 8
	headerBytes       = 64 << 10
	metadataBytes     = 1 << 20
	pdfScanBytes      = 4 << 20
)

// spreadRatio is how much wider than tall a page must be, in a book of portrait pages, to count as a double-page spread.
const spreadRatio = 1.2

// Pages are the facts of a file's images, measured from their headers.
type Pages struct {
	Count int
	// MedianLong and MedianShort are the median long and short edge of the pages that are not spreads; 0 when no page size is known.
	MedianLong, MedianShort int
	// Format is the commonest image format by extension ("jpg", "png", "webp", "avif"...).
	Format      string
	SpreadCount int
	// UnknownSize is true when no page size could be read (a PDF, or avif and jxl pages).
	UnknownSize bool
	Bytes       int64
}

// Facts is everything a probe learns about one file.
type Facts struct {
	Pages Pages
	// ComicInfo and MetronInfo are the embedded documents, nil when absent or unreadable.
	ComicInfo  *comicinfo.Info
	MetronInfo *comicinfo.Info
	// Sampled is true when the archive held more entries than were read in full.
	Sampled bool
}

var imageExtensions = map[string]string{".jpg": "jpg", ".jpeg": "jpg", ".png": "png", ".gif": "gif", ".webp": "webp", ".avif": "avif", ".jxl": "jxl"}

// Probe reads a comic file's page facts and embedded metadata without extracting it.
func Probe(ctx context.Context, fsys fsx.FS, filePath string) (Facts, error) {
	if strings.EqualFold(path.Ext(filePath), ".pdf") {
		return probePDF(fsys, filePath)
	}
	imagesRead := 0
	want := func(e archive.Entry) int64 {
		base := strings.ToLower(path.Base(e.Name))
		if isEmbeddedMetadata(e.Name, base) {
			return metadataBytes
		}
		if _, ok := imageExtensions[path.Ext(base)]; !ok {
			return 0
		}
		imagesRead++
		if imagesRead > fullHeaderEntries && imagesRead%sampleEvery != 0 {
			return 0
		}
		return headerBytes
	}
	entries, truncated, err := archive.Peek(ctx, fsys, filePath, want, maxProbeEntries)
	if err != nil {
		return Facts{}, err
	}
	facts := Facts{Sampled: truncated || imagesRead > fullHeaderEntries}
	var sizes []pageSize
	formats := map[string]int{}
	for _, p := range entries {
		base := strings.ToLower(path.Base(p.Entry.Name))
		switch {
		case isEmbeddedMetadata(p.Entry.Name, base) && p.Head != nil:
			readEmbedded(&facts, base, p.Head)
		case imageExtensions[path.Ext(base)] != "":
			facts.Pages.Count++
			facts.Pages.Bytes += p.Entry.SizeBytes
			formats[imageExtensions[path.Ext(base)]]++
			if p.Head == nil {
				continue
			}
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(p.Head)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
				sizes = append(sizes, pageSize{w: cfg.Width, h: cfg.Height})
			}
		}
	}
	facts.Pages.Format = commonest(formats)
	facts.Pages.fillMedians(sizes)
	return facts, nil
}

// isEmbeddedMetadata accepts the metadata documents at the archive root or one folder down.
func isEmbeddedMetadata(name, lowerBase string) bool {
	if lowerBase != "comicinfo.xml" && lowerBase != "metroninfo.xml" {
		return false
	}
	return strings.Count(strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/"), "/") <= 1
}

func readEmbedded(facts *Facts, lowerBase string, data []byte) {
	if lowerBase == "comicinfo.xml" {
		if info, err := comicinfo.ParseComicInfo(data); err == nil && facts.ComicInfo == nil {
			facts.ComicInfo = &info
		}
		return
	}
	if info, err := comicinfo.ParseMetronInfo(data); err == nil && facts.MetronInfo == nil {
		facts.MetronInfo = &info
	}
}

type pageSize struct{ w, h int }

// fillMedians sets the median edges of the pages that are not double-page spreads, and the spread and unknown-size facts.
func (p *Pages) fillMedians(sizes []pageSize) {
	if len(sizes) == 0 {
		p.UnknownSize = p.Count > 0
		return
	}
	portrait := 0
	for _, s := range sizes {
		if s.h > s.w {
			portrait++
		}
	}
	mostlyPortrait := portrait*2 > len(sizes)
	var longs, shorts []int
	for _, s := range sizes {
		if mostlyPortrait && float64(s.w) > spreadRatio*float64(s.h) {
			p.SpreadCount++
			continue
		}
		longs = append(longs, max(s.w, s.h))
		shorts = append(shorts, min(s.w, s.h))
	}
	if len(longs) == 0 {
		p.UnknownSize = true
		return
	}
	p.MedianLong, p.MedianShort = median(longs), median(shorts)
}

func median(values []int) int {
	sort.Ints(values)
	return values[len(values)/2]
}

// commonest returns the key with the highest count, the alphabetically first on a tie.
func commonest(counts map[string]int) string {
	best, bestCount := "", 0
	for k, n := range counts {
		if n > bestCount || n == bestCount && k < best {
			best, bestCount = k, n
		}
	}
	return best
}

var (
	pdfPagesNode = regexp.MustCompile(`/Type\s*/Pages\b`)
	pdfCount     = regexp.MustCompile(`/Count\s+(\d+)`)
)

// probePDF counts pages from the page-tree node when it is readable in the file's head and tail; a PDF that keeps it in a compressed object stream reports 0 pages.
func probePDF(fsys fsx.FS, filePath string) (Facts, error) {
	info, err := fsys.Stat(filePath)
	if err != nil {
		return Facts{}, err
	}
	file, err := fsys.Open(filePath)
	if err != nil {
		return Facts{}, err
	}
	defer func() { _ = file.Close() }()
	facts := Facts{Pages: Pages{Bytes: info.Size, UnknownSize: true, Format: "pdf"}}
	for _, window := range [][2]int64{{0, pdfScanBytes}, {max(0, info.Size-pdfScanBytes), pdfScanBytes}} {
		buf := make([]byte, window[1])
		n, err := file.ReadAt(buf, window[0])
		if err != nil && !errors.Is(err, io.EOF) {
			return facts, err
		}
		if count := pdfRootCount(buf[:n]); count > facts.Pages.Count {
			facts.Pages.Count = count
		}
	}
	return facts, nil
}

// pdfRootCount is the largest /Count of a /Pages dictionary: the root node counts every leaf page.
func pdfRootCount(data []byte) int {
	best := 0
	for _, loc := range pdfPagesNode.FindAllIndex(data, -1) {
		end := min(len(data), loc[1]+300)
		start := max(0, loc[0]-300)
		if m := pdfCount.FindSubmatch(data[start:end]); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil && n > best {
				best = n
			}
		}
	}
	return best
}
