package bookmeta_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/identify/bookmeta"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

const opf = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
  <dc:title> Invented Tale </dc:title>
  <dc:creator opf:role="aut">Mara Quell</dc:creator>
  <dc:creator opf:role="aut">Ivo Penn</dc:creator>
  <dc:language>en</dc:language>
  <dc:identifier opf:scheme="ISBN">978-0-306-40615-7</dc:identifier>
  <dc:identifier opf:scheme="AMAZON">B000INVENT</dc:identifier>
  <meta name="calibre:series" content="The Invented Cycle"/>
  <meta name="calibre:series_index" content="2.5"/>
 </metadata>
</package>`

var wantMeta = bookmeta.Meta{
	Title: "Invented Tale", Authors: []string{"Mara Quell", "Ivo Penn"}, ISBN: "9780306406157", ASIN: "B000INVENT",
	Language: "en", Series: "The Invented Cycle", SeriesIndex: 2.5,
}

func memory(t *testing.T) *fsx.Mem {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/lib/Mara Quell/Invented Tale", 0o755); err != nil {
		t.Fatal(err)
	}
	return fsys
}

func write(t *testing.T, fsys *fsx.Mem, name string, data []byte) {
	t.Helper()
	file, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func epub(t *testing.T, rootfile string, extraEntries int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	add := func(name, content string) {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte(content))
	}
	add("META-INF/container.xml", fmt.Sprintf(`<container><rootfiles><rootfile full-path="%s"/></rootfiles></container>`, rootfile))
	add("OEBPS/content.opf", opf)
	// The package file is also stored under the name the container points at, so only the refusal keeps it unread.
	if rootfile != "OEBPS/content.opf" {
		add(rootfile, opf)
	}
	for index := range extraEntries {
		add(fmt.Sprintf("pad/%d.txt", index), "")
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestRead_ACalibreSidecarNamesTheBookForAnyFormat(t *testing.T) {
	fsys := memory(t)
	write(t, fsys, "/lib/Mara Quell/Invented Tale/metadata.opf", []byte(opf))
	write(t, fsys, "/lib/Mara Quell/Invented Tale/Invented Tale - Mara Quell.pdf", []byte("pdf"))

	got, ok := bookmeta.Read(fsys, "/lib/Mara Quell/Invented Tale/Invented Tale - Mara Quell.pdf")
	if !ok || !reflect.DeepEqual(got, wantMeta) {
		t.Fatalf("Read = %+v, %v; want %+v", got, ok, wantMeta)
	}
}

func TestRead_AnEPUBsOwnPackageFileNamesTheBook(t *testing.T) {
	fsys := memory(t)
	write(t, fsys, "/lib/Mara Quell/Invented Tale/book.epub", epub(t, "OEBPS/content.opf", 0))

	got, ok := bookmeta.Read(fsys, "/lib/Mara Quell/Invented Tale/book.epub")
	if !ok || !reflect.DeepEqual(got, wantMeta) {
		t.Fatalf("Read = %+v, %v; want %+v", got, ok, wantMeta)
	}
}

func TestRead_RefusesARootfileOutsideTheArchiveAndAnArchiveWithTooManyEntries(t *testing.T) {
	fsys := memory(t)
	write(t, fsys, "/lib/Mara Quell/Invented Tale/escape.epub", epub(t, "../../OEBPS/content.opf", 0))
	write(t, fsys, "/lib/Mara Quell/Invented Tale/absolute.epub", epub(t, "/OEBPS/content.opf", 0))
	write(t, fsys, "/lib/Mara Quell/Invented Tale/bomb.epub", epub(t, "OEBPS/content.opf", 5001))
	for _, name := range []string{"escape.epub", "absolute.epub", "bomb.epub"} {
		if got, ok := bookmeta.Read(fsys, "/lib/Mara Quell/Invented Tale/"+name); ok {
			t.Errorf("%s: Read = %+v, want nothing", name, got)
		}
	}
}

func TestRead_OnlyAValidISBNCounts(t *testing.T) {
	fsys := memory(t)
	cases := []struct{ name, identifier, want string }{
		{"valid10", "0-306-40615-2", "0306406152"},
		{"bad10", "0-306-40615-3", ""},
		{"urn", "urn:isbn:9780306406157", "9780306406157"},
		{"short", "12345", ""},
	}
	for _, c := range cases {
		dir := "/lib/" + c.name
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, fsys, dir+"/metadata.opf", []byte(fmt.Sprintf(
			`<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>T</dc:title><dc:identifier>%s</dc:identifier></metadata></package>`, c.identifier)))
		got, ok := bookmeta.Read(fsys, dir+"/book.pdf")
		if !ok || got.ISBN != c.want {
			t.Errorf("%s: ISBN = %q (ok %v), want %q", c.name, got.ISBN, ok, c.want)
		}
	}
}
