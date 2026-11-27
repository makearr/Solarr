package ebookconv_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/ebookconv"
)

type noTools struct{}

func (noTools) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("none")
}
func (noTools) Lookup(string) error { return errors.New("not found") }

func entries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for i, file := range reader.File {
		opened, _ := file.Open()
		body, _ := io.ReadAll(opened)
		_ = opened.Close()
		if i == 0 && (file.Name != "mimetype" || file.Method != zip.Store) {
			t.Errorf("first entry = %q method %d, want mimetype stored", file.Name, file.Method)
		}
		out[file.Name] = string(body)
	}
	return out
}

func TestTextToEPUBMakesAReadableEpubWithEscapedParagraphs(t *testing.T) {
	var out bytes.Buffer
	if err := ebookconv.TextToEPUB("Zela & Moru", strings.NewReader("First <one>\n\nSecond"), &out); err != nil {
		t.Fatal(err)
	}
	got := entries(t, out.Bytes())
	if got["mimetype"] != "application/epub+zip" || !strings.Contains(got["OEBPS/chapter.xhtml"], "<p>First &lt;one&gt;</p>") || !strings.Contains(got["OEBPS/content.opf"], "Zela &amp; Moru") {
		t.Errorf("epub = %v", got)
	}
}

func TestRepackEPUBKeepsTheContentAndDropsDesktopLitter(t *testing.T) {
	var source bytes.Buffer
	writer := zip.NewWriter(&source)
	for _, name := range []string{"OEBPS/chapter.xhtml", ".DS_Store", "__MACOSX/x", "mimetype"} {
		entry, _ := writer.Create(name)
		_, _ = entry.Write([]byte("body of " + name))
	}
	_ = writer.Close()
	var out bytes.Buffer
	if err := ebookconv.RepackEPUB(bytes.NewReader(source.Bytes()), int64(source.Len()), &out); err != nil {
		t.Fatal(err)
	}
	got := entries(t, out.Bytes())
	if len(got) != 2 || got["OEBPS/chapter.xhtml"] != "body of OEBPS/chapter.xhtml" || got["mimetype"] != "application/epub+zip" {
		t.Errorf("repacked = %v", got)
	}
}

func TestPandocIsOptional(t *testing.T) {
	if err := ebookconv.ViaPandoc(t.Context(), noTools{}, "in.docx", "out.epub"); !errors.Is(err, ebookconv.ErrNeedsPandoc) {
		t.Errorf("without pandoc = %v, want ErrNeedsPandoc", err)
	}
	if _, viaPandoc := ebookconv.Formats(noTools{}); len(viaPandoc) != 0 {
		t.Errorf("pandoc formats without pandoc = %v", viaPandoc)
	}
}
