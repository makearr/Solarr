// Package comicrepack rewrites a comic archive (cbz, cbr, cb7, cbt) as a store-only cbz in natural page order,
// optionally converting the pages to one image format and bringing them down to a page size.
package comicrepack

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	webp "github.com/mayahiro/go-webp"

	"github.com/makearr/Solarr/internal/platform/archive"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/zipbest"
)

// DefaultQuality is the page quality when the aim names none.
const DefaultQuality = 80

// maxPageBytes is the largest page that is decoded; a bigger file is copied untouched.
const maxPageBytes int64 = 128 << 20

// scratchMargin is the free space the extraction must leave on the work disk.
const scratchMargin int64 = 64 << 20

// Aim is what the pages should become; the zero value only repacks.
type Aim struct {
	// Format is "webp" or "jpg"; anything else keeps every page's own format.
	Format  string
	Quality int
	// PageLong and PageShort cap a page's edges, never upscaling; 0 leaves that edge alone.
	PageLong, PageShort int
}

// Result counts what happened to the pages.
type Result struct {
	Entries, Pages, Reencoded, Downscaled int
}

// CanonicalFormat is the name of an image format as the aim and the probe spell it: "jpeg" is "jpg".
func CanonicalFormat(format string) string {
	format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), "."))
	if format == "jpeg" {
		return "jpg"
	}
	return format
}

// EdgeFor is the longest edge a width by height page gets under the aim: the page itself when it already fits.
func EdgeFor(width, height int, aim Aim) int {
	long, short := max(width, height), min(width, height)
	scale := 1.0
	if aim.PageLong > 0 && long > aim.PageLong {
		scale = min(scale, float64(aim.PageLong)/float64(long))
	}
	if aim.PageShort > 0 && short > aim.PageShort {
		scale = min(scale, float64(aim.PageShort)/float64(short))
	}
	return max(1, int(float64(long)*scale))
}

// Repack extracts source into scratch (removed again), then writes output as a cbz; output is only replaced whole.
func Repack(ctx context.Context, fsys fsx.FS, source, output, scratch string, aim Aim, progress func(done, total int)) (Result, error) {
	if err := fsys.MkdirAll(scratch, 0o755); err != nil {
		return Result{}, fmt.Errorf("comicrepack: scratch folder: %w", err)
	}
	defer removeTree(fsys, scratch)
	limits := archive.DefaultLimits()
	limits.MinFreeBytes = scratchMargin
	if err := archive.ExtractComic(ctx, fsys, source, scratch, limits); err != nil {
		return Result{}, fmt.Errorf("comicrepack: unpack: %w", err)
	}
	files, err := listFiles(fsys, scratch, "")
	if err != nil {
		return Result{}, err
	}
	slices.SortStableFunc(files, func(a, b entry) int { return naturalCompare(strings.ToLower(a.rel), strings.ToLower(b.rel)) })
	var result Result
	used := map[string]bool{}
	err = fsx.WriteAtomic(fsys, output, func(out io.Writer) error {
		writer := zipbest.NewWriter(out)
		for index, file := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			name, body, how, err := page(fsys, file, aim, used)
			if err != nil {
				return err
			}
			used[strings.ToLower(name)] = true
			member, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: file.info.ModTime})
			if err != nil {
				return err
			}
			if _, err := member.Write(body); err != nil {
				return err
			}
			result.Entries++
			if how != notAPage {
				result.Pages++
			}
			if how == reencoded || how == downscaled {
				result.Reencoded++
			}
			if how == downscaled {
				result.Downscaled++
			}
			if progress != nil {
				progress(index+1, len(files))
			}
		}
		return writer.Close()
	})
	return result, err
}

type entry struct {
	rel  string
	abs  string
	info fsx.FileInfo
}

// listFiles walks the extracted tree; macOS and desktop litter is left behind.
func listFiles(fsys fsx.FS, root, rel string) ([]entry, error) {
	entries, err := fsys.ReadDir(path.Join(root, rel))
	if err != nil {
		return nil, fmt.Errorf("comicrepack: read %q: %w", rel, err)
	}
	var out []entry
	for _, each := range entries {
		name := each.Name
		if strings.HasPrefix(name, ".") || name == "__MACOSX" || strings.EqualFold(name, "thumbs.db") {
			continue
		}
		child := path.Join(rel, name)
		if each.IsDir {
			more, err := listFiles(fsys, root, child)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
			continue
		}
		info, err := fsys.Stat(path.Join(root, child))
		if err != nil {
			return nil, fmt.Errorf("comicrepack: stat %q: %w", child, err)
		}
		out = append(out, entry{rel: child, abs: path.Join(root, child), info: info})
	}
	return out, nil
}

func removeTree(fsys fsx.FS, dir string) {
	entries, err := fsys.ReadDir(dir)
	if err == nil {
		for _, each := range entries {
			if each.IsDir {
				removeTree(fsys, path.Join(dir, each.Name))
			} else {
				_ = fsys.Remove(path.Join(dir, each.Name))
			}
		}
	}
	_ = fsys.Remove(dir)
}

type outcome int

const (
	notAPage outcome = iota
	kept
	reencoded
	downscaled
)

var pageFormats = map[string]string{".jpg": "jpg", ".jpeg": "jpg", ".png": "png", ".webp": "webp"}

// page is one entry's name and bytes as they go into the new archive.
func page(fsys fsx.FS, file entry, aim Aim, used map[string]bool) (string, []byte, outcome, error) {
	original := func() (string, []byte, outcome, error) {
		body, err := readAll(fsys, file.abs)
		return file.rel, body, kept, err
	}
	ext := strings.ToLower(path.Ext(file.rel))
	format, known := pageFormats[ext]
	if !known {
		name, body, _, err := original()
		if imageOnly[ext] {
			return name, body, kept, err
		}
		return name, body, notAPage, err
	}
	want := CanonicalFormat(aim.Format)
	if want != "webp" && want != "jpg" {
		want = format
	}
	if file.info.Size > maxPageBytes {
		return original()
	}
	body, err := readAll(fsys, file.abs)
	if err != nil {
		return "", nil, kept, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return file.rel, body, kept, nil //nolint:nilerr // a page that cannot be read is carried over as it is
	}
	edge := EdgeFor(config.Width, config.Height, aim)
	shrinks := edge < max(config.Width, config.Height)
	if want == format && !shrinks {
		return file.rel, body, kept, nil
	}
	decoded, err := artwork.Decode(body)
	if err != nil {
		return file.rel, body, kept, nil //nolint:nilerr // see above
	}
	if shrinks {
		decoded = artwork.ResizeLongestEdge(decoded, edge)
	}
	encoded, err := encode(decoded, want, aim.Quality)
	if err != nil || (!shrinks && len(encoded) >= len(body)) {
		return file.rel, body, kept, nil //nolint:nilerr // a page the encoder cannot improve keeps its bytes
	}
	name := strings.TrimSuffix(file.rel, path.Ext(file.rel)) + "." + want
	if want == format {
		name = file.rel
	}
	if name != file.rel && used[strings.ToLower(name)] {
		return file.rel, body, kept, nil
	}
	if shrinks {
		return name, encoded, downscaled, nil
	}
	return name, encoded, reencoded, nil
}

// imageOnly are page-like files that are carried over, counted as pages, and never decoded.
var imageOnly = map[string]bool{".gif": true, ".avif": true, ".jxl": true, ".bmp": true, ".tif": true, ".tiff": true}

func encode(img image.Image, format string, quality int) ([]byte, error) {
	if quality <= 0 {
		quality = DefaultQuality
	}
	if format == "jpg" {
		return artwork.EncodeJPEGAt(img, quality)
	}
	var buf bytes.Buffer
	if format == "png" {
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("comicrepack: encode png: %w", err)
		}
		return buf.Bytes(), nil
	}
	if err := webp.Encode(&buf, img, &webp.Options{Compression: webp.CompressionLossy, Quality: quality}); err != nil {
		return nil, fmt.Errorf("comicrepack: encode webp: %w", err)
	}
	return buf.Bytes(), nil
}

func readAll(fsys fsx.FS, abs string) ([]byte, error) {
	file, err := fsys.Open(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

// naturalCompare orders names as a person numbers them: "2" before "10".
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			na, _ := strconv.ParseInt(da, 10, 64)
			nb, _ := strconv.ParseInt(db, 10, 64)
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return strings.Compare(a[:1], b[:1])
		}
		a, b = a[1:], b[1:]
	}
	return strings.Compare(a, b)
}

func digits(text string) string {
	end := 0
	for end < len(text) && end < 18 && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	return text[:end]
}
