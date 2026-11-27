// Package ebookconv converts ebooks: epub written and repacked in pure Go, and every other format through pandoc when
// the image carries it (the -full variant); a machine without pandoc simply offers fewer conversions.
package ebookconv

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"path"
	"strings"
)

// Runner runs an external tool; mediatool.Runner has this shape.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	Lookup(name string) error
}

// ErrNeedsPandoc says the conversion asked for is one only pandoc does, and this machine has none.
var ErrNeedsPandoc = errors.New("ebookconv: this conversion needs pandoc, which is not installed (the -full image carries it)")

const (
	pandoc    = "pandoc"
	container = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
)

// Formats says what the machine converts into epub: the formats pure Go reads, and every pandoc input when pandoc exists.
func Formats(runner Runner) (viaGo, viaPandoc []string) {
	viaGo = []string{"txt", "html", "htm", "epub"}
	if runner != nil && runner.Lookup(pandoc) == nil {
		viaPandoc = []string{"docx", "odt", "md", "markdown", "rst", "fb2"}
	}
	return viaGo, viaPandoc
}

// TextToEPUB writes a plain text as a one-chapter epub; blank lines separate paragraphs.
func TextToEPUB(title string, text io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(text, 64<<20))
	if err != nil {
		return fmt.Errorf("ebookconv: read text: %w", err)
	}
	var body strings.Builder
	for _, paragraph := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n") {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" {
			body.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(paragraph), "\n", "<br/>") + "</p>\n")
		}
	}
	return HTMLToEPUB(title, strings.NewReader(body.String()), out)
}

// HTMLToEPUB writes an html fragment (or page body) as a one-chapter epub.
func HTMLToEPUB(title string, markup io.Reader, out io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(markup, 64<<20))
	if err != nil {
		return fmt.Errorf("ebookconv: read markup: %w", err)
	}
	chapter := `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + html.EscapeString(title) + `</title></head><body>` + string(raw) + `</body></html>`
	opf := `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` +
		html.EscapeString(title) + `</dc:title><dc:identifier id="id">solarr-` + fmt.Sprint(len(raw)) + `</dc:identifier><dc:language>en</dc:language></metadata><manifest><item id="c" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`
	writer := zip.NewWriter(out)
	if err := writeStored(writer, "mimetype", []byte("application/epub+zip")); err != nil {
		return err
	}
	for name, content := range map[string]string{"META-INF/container.xml": container, "OEBPS/content.opf": opf, "OEBPS/chapter.xhtml": chapter} {
		if err := writeDeflated(writer, name, []byte(content)); err != nil {
			return err
		}
	}
	return writer.Close()
}

// RepackEPUB copies an epub with its mimetype first and stored, as readers require, and without the litter desktop
// tools leave in an archive.
func RepackEPUB(source io.ReaderAt, size int64, out io.Writer) error {
	reader, err := zip.NewReader(source, size)
	if err != nil {
		return fmt.Errorf("ebookconv: open epub: %w", err)
	}
	writer := zip.NewWriter(out)
	if err := writeStored(writer, "mimetype", []byte("application/epub+zip")); err != nil {
		return err
	}
	for _, file := range reader.File {
		name := file.Name
		if name == "mimetype" || file.FileInfo().IsDir() || litter(name) {
			continue
		}
		content, err := readFile(file)
		if err != nil {
			return err
		}
		if err := writeDeflated(writer, name, content); err != nil {
			return err
		}
	}
	return writer.Close()
}

func litter(name string) bool {
	base := path.Base(name)
	return strings.HasPrefix(name, "__MACOSX/") || base == ".DS_Store" || base == "Thumbs.db"
}

func readFile(file *zip.File) ([]byte, error) {
	opened, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("ebookconv: open %s: %w", file.Name, err)
	}
	defer func() { _ = opened.Close() }()
	content, err := io.ReadAll(io.LimitReader(opened, 512<<20))
	if err != nil {
		return nil, fmt.Errorf("ebookconv: read %s: %w", file.Name, err)
	}
	return content, nil
}

func writeStored(writer *zip.Writer, name string, content []byte) error {
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return fmt.Errorf("ebookconv: write %s: %w", name, err)
	}
	_, err = entry.Write(content)
	return err
}

func writeDeflated(writer *zip.Writer, name string, content []byte) error {
	entry, err := writer.Create(name)
	if err != nil {
		return fmt.Errorf("ebookconv: write %s: %w", name, err)
	}
	_, err = entry.Write(content)
	return err
}

// ViaPandoc converts a file pandoc reads to the format its output name asks for; ErrNeedsPandoc when there is none.
func ViaPandoc(ctx context.Context, runner Runner, input, output string) error {
	if runner == nil || runner.Lookup(pandoc) != nil {
		return ErrNeedsPandoc
	}
	if output == "" || bytes.ContainsAny([]byte(output), "\x00") {
		return errors.New("ebookconv: no output file")
	}
	if _, err := runner.Run(ctx, pandoc, "--from", "auto", "-o", output, "--", input); err != nil {
		return fmt.Errorf("ebookconv: pandoc: %w", err)
	}
	return nil
}
