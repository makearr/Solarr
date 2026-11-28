package artwork

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"

	webp "github.com/mayahiro/go-webp"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Format is the one image format stored item pictures and video previews are written in (Settings -> Database).
type Format string

// The formats a person can choose; WebP is the default.
const (
	FormatWebP Format = "webp"
	FormatJPEG Format = "jpg"
	FormatPNG  Format = "png"
)

// Formats lists every format, the default first.
var Formats = []Format{FormatWebP, FormatJPEG, FormatPNG}

// ParseFormat reads a format name.
func ParseFormat(name string) (Format, bool) {
	for _, format := range Formats {
		if string(format) == name {
			return format, true
		}
	}
	return "", false
}

// Ext is the file extension of the format, with its dot.
func (f Format) Ext() string { return "." + string(f) }

// ContentType is the media type of the format.
func (f Format) ContentType() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	case FormatPNG:
		return "image/png"
	}
	return "image/webp"
}

// Ext is the extension new pictures are written with.
func Ext() string { return Current().Format.Ext() }

// ContentTypeOfPath is the media type of a stored picture, by its extension.
func ContentTypeOfPath(path string) string {
	if format, ok := ParseFormat(strings.TrimPrefix(filepath.Ext(path), ".")); ok {
		return format.ContentType()
	}
	return FormatWebP.ContentType()
}

// Preferred lists the formats in the order a stored picture is looked for: the one in force, then the others, since a
// conversion after a change takes time and the old files stay readable until it reaches them.
func Preferred() []Format {
	current := Current().Format
	order := []Format{current}
	for _, format := range Formats {
		if format != current {
			order = append(order, format)
		}
	}
	return order
}

// EncodeAt writes img in a format; quality applies to the lossy ones (WebP, JPEG), PNG is lossless.
func EncodeAt(img image.Image, format Format, quality int) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch format {
	case FormatJPEG:
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	case FormatPNG:
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
	default:
		err = webp.Encode(&buf, img, &webp.Options{Compression: webp.CompressionLossy, Quality: quality})
	}
	if err != nil {
		return nil, fmt.Errorf("artwork: encode %s: %w", format, err)
	}
	return buf.Bytes(), nil
}

// Encode writes img in the format and quality in force for stored pictures.
func Encode(img image.Image) ([]byte, error) {
	return EncodeAt(img, Current().Format, Current().Quality)
}

// FindFile is the path of a stored picture of a stem (a name without its extension): in the format in force when it is
// there, else in another, in its shard folder or, for one the move to shards has not reached, the folder itself.
// found is false when none exists; the path is then where a picture would be written.
func FindFile(fsys fsx.FS, dir, stem string) (path string, found bool) {
	for _, format := range Preferred() {
		name := stem + format.Ext()
		if sharded := CoverPath(dir, name); fileExists(fsys, sharded) {
			return sharded, true
		}
		if legacy := filepath.Join(dir, name); fileExists(fsys, legacy) {
			return legacy, true
		}
	}
	return CoverPath(dir, stem+Ext()), false
}

// EncodeWebP writes img as lossy WebP at the quality in force, whatever format is in force.
func EncodeWebP(img image.Image) ([]byte, error) { return EncodeAt(img, FormatWebP, Current().Quality) }
