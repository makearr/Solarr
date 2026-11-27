package comicinfo

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Modes of writing ComicInfo.xml into a file that may already carry one.
const (
	// ModeFill adds the elements a file lacks and never changes one it has.
	ModeFill = "fill"
	// ModeReplace sets every element Solarr has a value for.
	ModeReplace = "replace"
)

// ComicInfoName is the entry a reader looks for at the root of a cbz.
const ComicInfoName = "ComicInfo.xml"

// element is one child of the ComicInfo root with its text.
type element struct{ name, text string }

// knownElements are the ComicInfo elements Solarr knows for an issue, in the schema's order; an empty value is left out.
func knownElements(info Info) []element {
	number := func(n int) string {
		if n <= 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	out := []element{
		{"Title", info.Title}, {"Series", info.Series}, {"Number", info.Number}, {"Count", number(info.Count)}, {"Volume", number(info.Volume)},
		{"Summary", info.Summary}, {"Notes", info.Notes}, {"Year", number(info.Year)}, {"Month", number(info.Month)}, {"Day", number(info.Day)},
	}
	for _, role := range []struct{ element, key string }{{"Writer", "writer"}, {"Penciller", "penciller"}, {"Inker", "inker"}, {"Colorist", "colorist"}, {"Letterer", "letterer"}, {"CoverArtist", "cover_artist"}, {"Editor", "editor"}} {
		out = append(out, element{role.element, strings.Join(info.Credits[role.key], ", ")})
	}
	out = append(out, element{"Publisher", info.Publisher}, element{"Imprint", info.Imprint}, element{"Genre", info.Genre}, element{"Web", info.Web},
		element{"PageCount", number(info.PageCount)}, element{"LanguageISO", info.Language}, element{"Manga", info.Manga}, element{"AgeRating", info.AgeRating},
		element{"StoryArc", info.StoryArc}, element{"SeriesGroup", info.SeriesGroup})
	kept := out[:0]
	for _, e := range out {
		if strings.TrimSpace(e.text) != "" {
			kept = append(kept, e)
		}
	}
	return kept
}

// Merge answers the ComicInfo.xml a file should carry and whether it differs from existing (nil for none): ModeFill
// keeps every element the file has, ModeReplace overwrites those Solarr has a value for; elements it does not know stay.
func Merge(existing []byte, info Info, mode string) ([]byte, bool, error) {
	current, others, err := readElements(existing)
	if err != nil {
		return nil, false, err
	}
	changed := existing == nil
	index := map[string]int{}
	for i, e := range current {
		index[e.name] = i
	}
	for _, want := range knownElements(info) {
		at, found := index[want.name]
		switch {
		case !found:
			index[want.name] = len(current)
			current = append(current, want)
			changed = true
		case mode == ModeReplace && strings.TrimSpace(current[at].text) != want.text:
			current[at].text = want.text
			changed = true
		case strings.TrimSpace(current[at].text) == "":
			current[at].text = want.text
			changed = true
		}
	}
	if !changed {
		return existing, false, nil
	}
	var buffer bytes.Buffer
	buffer.WriteString(xml.Header)
	buffer.WriteString("<ComicInfo xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\">\n")
	for _, e := range current {
		buffer.WriteString("  <" + e.name + ">")
		if err := xml.EscapeText(&buffer, []byte(e.text)); err != nil {
			return nil, false, err
		}
		buffer.WriteString("</" + e.name + ">\n")
	}
	for _, other := range others {
		buffer.WriteString("  " + other + "\n")
	}
	buffer.WriteString("</ComicInfo>\n")
	return buffer.Bytes(), true, nil
}

// readElements lists the text-only children of the root; any other child (pages, structured elements) is kept verbatim.
func readElements(data []byte) (elements []element, others []string, err error) {
	if len(data) == 0 {
		return nil, nil, nil
	}
	decoder, err := newDecoder(data)
	if err != nil {
		return nil, nil, err
	}
	depth := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return elements, others, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("comicinfo: %w", err)
		}
		start, isStart := token.(xml.StartElement)
		if !isStart {
			if _, isEnd := token.(xml.EndElement); isEnd {
				depth--
			}
			continue
		}
		depth++
		if depth != 2 {
			continue
		}
		var text struct {
			Text  string `xml:",chardata"`
			Inner string `xml:",innerxml"`
		}
		if err := decoder.DecodeElement(&text, &start); err != nil {
			return nil, nil, fmt.Errorf("comicinfo: %w", err)
		}
		depth--
		if strings.Contains(text.Inner, "<") {
			others = append(others, "<"+start.Name.Local+">"+text.Inner+"</"+start.Name.Local+">")
			continue
		}
		elements = append(elements, element{start.Name.Local, text.Text})
	}
}

// RewriteCBZ copies a zip entry by entry, compressed bytes untouched and in the same order and method, with ComicInfo.xml
// replaced by data (added last when the archive had none).
func RewriteCBZ(source io.ReaderAt, size int64, destination io.Writer, data []byte) error {
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return fmt.Errorf("comicinfo: open cbz: %w", err)
	}
	writer := zip.NewWriter(destination)
	placed := false
	for _, file := range reader.File {
		if file.Name == ComicInfoName {
			if err := addEntry(writer, file.FileHeader, data); err != nil {
				return err
			}
			placed = true
			continue
		}
		raw, err := file.OpenRaw()
		if err != nil {
			return fmt.Errorf("comicinfo: read %s: %w", file.Name, err)
		}
		header := file.FileHeader
		out, err := writer.CreateRaw(&header)
		if err != nil {
			return fmt.Errorf("comicinfo: write %s: %w", file.Name, err)
		}
		if _, err := io.Copy(out, raw); err != nil {
			return fmt.Errorf("comicinfo: copy %s: %w", file.Name, err)
		}
	}
	if !placed {
		if err := addEntry(writer, zip.FileHeader{Name: ComicInfoName, Method: zip.Deflate}, data); err != nil {
			return err
		}
	}
	return writer.Close()
}

func addEntry(writer *zip.Writer, header zip.FileHeader, data []byte) error {
	header.UncompressedSize64, header.CompressedSize64, header.CRC32 = 0, 0, 0
	out, err := writer.CreateHeader(&header)
	if err != nil {
		return fmt.Errorf("comicinfo: write %s: %w", header.Name, err)
	}
	_, err = out.Write(data)
	return err
}

// SeriesJSON is Mylar's series.json for a series, the fields Kavita and Komga read.
func SeriesJSON(series Series) ([]byte, error) {
	metadata := map[string]any{"type": "comicSeries", "name": series.Name, "publisher": series.Publisher, "year": series.Year,
		"description_text": series.Description, "booktype": series.Type}
	if series.ComicVineID != "" {
		metadata["comicid"] = series.ComicVineID
	}
	return json.MarshalIndent(map[string]any{"version": "1.0.1", "metadata": metadata}, "", "  ")
}

// ReadFromCBZ answers the ComicInfo.xml a cbz carries at its root, nil when it has none.
func ReadFromCBZ(source io.ReaderAt, size int64) ([]byte, error) {
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return nil, fmt.Errorf("comicinfo: open cbz: %w", err)
	}
	for _, file := range reader.File {
		if file.Name != ComicInfoName {
			continue
		}
		body, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("comicinfo: read %s: %w", ComicInfoName, err)
		}
		defer func() { _ = body.Close() }()
		return io.ReadAll(io.LimitReader(body, maxXMLBytes))
	}
	return nil, nil
}
