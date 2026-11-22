package comic

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/identify/comicinfo"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// The fixtures hold three PNG pages (two 80x120, one 200x120 spread) and a ComicInfo.xml, one per container.
func TestProbeEveryContainer(t *testing.T) {
	wantInfo := comicinfo.Info{Series: "Night Harbor", Number: "1", Year: 2023, PageCount: 3, Credits: map[string][]string{}}
	for _, name := range []string{"sample.cbz", "sample.cbr", "sample.cb7", "sample.cbt"} {
		t.Run(name, func(t *testing.T) {
			got, err := Probe(context.Background(), fsx.NewOS(), filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			wantPages := Pages{Count: 3, MedianLong: 120, MedianShort: 80, Format: "png", SpreadCount: 1, Bytes: 644}
			if got.Pages != wantPages {
				t.Fatalf("pages %+v, want %+v", got.Pages, wantPages)
			}
			if got.ComicInfo == nil || !equalInfo(*got.ComicInfo, wantInfo) || got.MetronInfo != nil || got.Sampled {
				t.Fatalf("facts %+v", got)
			}
		})
	}
}

func equalInfo(a, b comicinfo.Info) bool {
	return a.Series == b.Series && a.Number == b.Number && a.Year == b.Year && a.PageCount == b.PageCount && len(a.IDs) == 0 && len(b.IDs) == 0
}

func TestProbeTruncatedArchiveIsAnError(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.cbz"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cut.cbz", "cut.cbr", "cut.cb7", "cut.cbt"} {
		cut := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(cut, data[:len(data)/2], 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Probe(context.Background(), fsx.NewOS(), cut); err == nil && name != "cut.cbt" {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestPagesSpreadsStayOutOfTheMedians(t *testing.T) {
	var p Pages
	p.Count = 5
	p.fillMedians([]pageSize{{100, 150}, {100, 150}, {120, 180}, {300, 150}, {100, 150}})
	want := Pages{Count: 5, MedianLong: 150, MedianShort: 100, SpreadCount: 1}
	if p != want {
		t.Fatalf("got %+v want %+v", p, want)
	}
	var landscape Pages
	landscape.Count = 2
	landscape.fillMedians([]pageSize{{300, 150}, {300, 150}})
	if want := (Pages{Count: 2, MedianLong: 300, MedianShort: 150}); landscape != want {
		t.Fatalf("landscape book: got %+v want %+v", landscape, want)
	}
	var unknown Pages
	unknown.Count = 4
	unknown.fillMedians(nil)
	if !unknown.UnknownSize {
		t.Fatalf("no sizes: got %+v", unknown)
	}
}

func TestPDFRootCount(t *testing.T) {
	doc := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Pages /Count 3 /Kids [2 0 R] >>\nendobj\n2 0 obj\n<< /Type /Pages /Parent 1 0 R /Count 2 >>\nendobj\n")
	if got := pdfRootCount(doc); got != 3 {
		t.Fatalf("got %d want 3", got)
	}
	if got := pdfRootCount([]byte("not a pdf")); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
}
