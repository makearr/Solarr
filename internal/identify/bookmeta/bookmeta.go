// Package bookmeta reads what a book says of itself: the title, author and ids in a Calibre metadata.opf beside it or
// in an EPUB's own package file.
package bookmeta

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Limits on what is read from an untrusted file.
const (
	maxContainerBytes = 64 << 10
	maxPackageBytes   = 1 << 20
	maxZipEntries     = 5000
)

// Meta is what the file says; every field may be empty.
type Meta struct {
	Title       string
	Authors     []string
	ISBN        string
	ASIN        string
	Language    string
	Series      string
	SeriesIndex float64
}

// Read answers the metadata of the book at absPath: a metadata.opf in its folder first, else, for an EPUB, the
// package file inside it. ok is false when neither names anything.
func Read(fsys fsx.FS, absPath string) (Meta, bool) {
	if meta, ok := readSidecar(fsys, path.Join(path.Dir(absPath), "metadata.opf")); ok {
		return meta, true
	}
	if strings.EqualFold(path.Ext(absPath), ".epub") {
		return readEPUB(fsys, absPath)
	}
	return Meta{}, false
}

func readSidecar(fsys fsx.FS, opfPath string) (Meta, bool) {
	file, err := fsys.Open(opfPath)
	if err != nil {
		return Meta{}, false
	}
	defer func() { _ = file.Close() }()
	return parsePackage(io.LimitReader(file, maxPackageBytes))
}

func readEPUB(fsys fsx.FS, epubPath string) (Meta, bool) {
	info, err := fsys.Stat(epubPath)
	if err != nil {
		return Meta{}, false
	}
	file, err := fsys.Open(epubPath)
	if err != nil {
		return Meta{}, false
	}
	defer func() { _ = file.Close() }()
	archive, err := zip.NewReader(file, info.Size)
	if err != nil || len(archive.File) > maxZipEntries {
		return Meta{}, false
	}
	container, ok := readEntry(archive, "META-INF/container.xml", maxContainerBytes)
	if !ok {
		return Meta{}, false
	}
	var parsed struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if xml.Unmarshal(container, &parsed) != nil || len(parsed.Rootfiles) == 0 {
		return Meta{}, false
	}
	root := parsed.Rootfiles[0].FullPath
	if root == "" || strings.HasPrefix(root, "/") || strings.Contains(root, "..") {
		return Meta{}, false
	}
	pkg, ok := readEntry(archive, root, maxPackageBytes)
	if !ok {
		return Meta{}, false
	}
	return parsePackage(strings.NewReader(string(pkg)))
}

// readEntry reads at most limit bytes of the named entry.
func readEntry(archive *zip.Reader, name string, limit int64) ([]byte, bool) {
	for _, entry := range archive.File {
		if entry.Name != name {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, false
		}
		defer func() { _ = reader.Close() }()
		data, err := io.ReadAll(io.LimitReader(reader, limit))
		return data, err == nil
	}
	return nil, false
}

type opfPackage struct {
	Metadata struct {
		Titles      []string `xml:"title"`
		Creators    []string `xml:"creator"`
		Languages   []string `xml:"language"`
		Identifiers []struct {
			Scheme string `xml:"scheme,attr"`
			Value  string `xml:",chardata"`
		} `xml:"identifier"`
		Metas []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
}

func parsePackage(reader io.Reader) (Meta, bool) {
	var pkg opfPackage
	if xml.NewDecoder(reader).Decode(&pkg) != nil {
		return Meta{}, false
	}
	var meta Meta
	if len(pkg.Metadata.Titles) > 0 {
		meta.Title = strings.TrimSpace(pkg.Metadata.Titles[0])
	}
	for _, creator := range pkg.Metadata.Creators {
		if name := strings.TrimSpace(creator); name != "" {
			meta.Authors = append(meta.Authors, name)
		}
	}
	if len(pkg.Metadata.Languages) > 0 {
		meta.Language = strings.TrimSpace(pkg.Metadata.Languages[0])
	}
	for _, identifier := range pkg.Metadata.Identifiers {
		value := strings.TrimSpace(identifier.Value)
		switch scheme := strings.ToUpper(identifier.Scheme); {
		case scheme == "ASIN" || scheme == "MOBI-ASIN" || scheme == "AMAZON":
			if meta.ASIN == "" {
				meta.ASIN = value
			}
		default:
			if isbn := isbnOf(value); isbn != "" && meta.ISBN == "" {
				meta.ISBN = isbn
			}
		}
	}
	for _, one := range pkg.Metadata.Metas {
		switch one.Name {
		case "calibre:series":
			meta.Series = strings.TrimSpace(one.Content)
		case "calibre:series_index":
			meta.SeriesIndex, _ = strconv.ParseFloat(strings.TrimSpace(one.Content), 64)
		}
	}
	return meta, meta.Title != "" || len(meta.Authors) > 0 || meta.ISBN != "" || meta.ASIN != ""
}

// isbnOf is the identifier as a bare ISBN-13 or a valid ISBN-10, empty when it is neither.
func isbnOf(identifier string) string {
	value := strings.ToLower(identifier)
	value = strings.TrimPrefix(value, "urn:isbn:")
	value = strings.NewReplacer("-", "", " ", "").Replace(strings.ToUpper(value))
	switch len(value) {
	case 13:
		if (strings.HasPrefix(value, "978") || strings.HasPrefix(value, "979")) && allDigits(value) {
			return value
		}
	case 10:
		if validISBN10(value) {
			return value
		}
	}
	return ""
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func validISBN10(value string) bool {
	sum := 0
	for index, r := range value {
		var digit int
		switch {
		case r >= '0' && r <= '9':
			digit = int(r - '0')
		case r == 'X' && index == 9:
			digit = 10
		default:
			return false
		}
		sum += digit * (10 - index)
	}
	return sum%11 == 0
}
