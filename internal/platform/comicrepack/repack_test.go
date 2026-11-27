package comicrepack_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/comicrepack"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// noisyPNG is a picture that does not compress, so a lossy re-encode is clearly smaller.
func noisyPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(7))
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: uint8(random.Intn(256)), G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type member struct {
	name string
	body []byte
}

func zipOf(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, each := range members {
		out, err := writer.Create(each.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = out.Write(each.body)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarOf(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, each := range members {
		if err := writer.WriteHeader(&tar.Header{Name: each.name, Mode: 0o644, Size: int64(len(each.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = writer.Write(each.body)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func put(t *testing.T, fsys *fsx.Mem, absolute string, body []byte) {
	t.Helper()
	if err := fsys.MkdirAll("/lib", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(body)
	_ = file.Close()
}

func readZip(t *testing.T, fsys *fsx.Mem, absolute string) map[string][]byte {
	t.Helper()
	file, err := fsys.Open(absolute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	var order []string
	for _, each := range reader.File {
		if each.Method != zip.Deflate {
			t.Errorf("%s is not deflated", each.Name)
		}
		body, _ := each.Open()
		out[each.Name], _ = io.ReadAll(body)
		order = append(order, each.Name)
	}
	out["\x00order"] = []byte(joined(order))
	return out
}

func joined(names []string) string {
	var out string
	for _, name := range names {
		out += name + "|"
	}
	return out
}

func newFS() *fsx.Mem { return fsx.NewMem(clock.NewFake(time.Unix(1_700_000_000, 0))) }

func TestRepack_TarComicBecomesADeflatedZipInNaturalOrder(t *testing.T) {
	fsys := newFS()
	info := []byte("<ComicInfo/>")
	put(t, fsys, "/lib/issue.cbt", tarOf(t, []member{
		{"10.png", []byte("ten")}, {"2.png", []byte("two")}, {"ComicInfo.xml", info}, {".DS_Store", []byte("x")}, {"1.png", []byte("one")},
	}))
	result, err := comicrepack.Repack(context.Background(), fsys, "/lib/issue.cbt", "/lib/issue.cbz", "/lib/.work", comicrepack.Aim{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := readZip(t, fsys, "/lib/issue.cbz")
	if want := "1.png|2.png|10.png|ComicInfo.xml|"; string(got["\x00order"]) != want {
		t.Errorf("order = %q, want %q", got["\x00order"], want)
	}
	if string(got["ComicInfo.xml"]) != "<ComicInfo/>" || string(got["2.png"]) != "two" {
		t.Errorf("bytes were changed: %q %q", got["ComicInfo.xml"], got["2.png"])
	}
	// The pages here are not decodable images; they are carried over, counted as pages, never re-encoded.
	if want := (comicrepack.Result{Entries: 4, Pages: 3}); result != want {
		t.Errorf("result = %+v, want %+v", result, want)
	}
	if _, err := fsys.Stat("/lib/.work"); err == nil {
		t.Error("the scratch folder was left behind")
	}
}

func TestRepack_ReencodesToWebPAndRenamesThePage(t *testing.T) {
	fsys := newFS()
	page := noisyPNG(t, 120, 180)
	put(t, fsys, "/lib/a.cbz", zipOf(t, []member{{"p2.png", page}, {"p1.png", page}, {"notes.txt", []byte("n")}}))
	result, err := comicrepack.Repack(context.Background(), fsys, "/lib/a.cbz", "/lib/out.cbz", "/lib/.work", comicrepack.Aim{Format: "webp", Quality: 60}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := readZip(t, fsys, "/lib/out.cbz")
	if want := "notes.txt|p1.webp|p2.webp|"; string(got["\x00order"]) != want {
		t.Errorf("order = %q, want %q", got["\x00order"], want)
	}
	if len(got["p1.webp"]) == 0 || len(got["p1.webp"]) >= len(page) {
		t.Errorf("webp page is %d bytes, the png was %d", len(got["p1.webp"]), len(page))
	}
	if want := (comicrepack.Result{Entries: 3, Pages: 2, Reencoded: 2}); result != want {
		t.Errorf("result = %+v, want %+v", result, want)
	}
}

func TestRepack_DownscalesToThePageSizeAndNeverUpscales(t *testing.T) {
	fsys := newFS()
	big, small := noisyPNG(t, 400, 600), noisyPNG(t, 100, 150)
	put(t, fsys, "/lib/a.cbz", zipOf(t, []member{{"1.png", big}, {"2.png", small}}))
	result, err := comicrepack.Repack(context.Background(), fsys, "/lib/a.cbz", "/lib/out.cbz", "/lib/.work", comicrepack.Aim{PageLong: 300, PageShort: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := readZip(t, fsys, "/lib/out.cbz")
	config, format, err := image.DecodeConfig(bytes.NewReader(got["1.png"]))
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" {
		t.Errorf("the page named .png holds %s", format)
	}
	if config.Width != 200 || config.Height != 300 {
		t.Errorf("big page is %dx%d, want 200x300", config.Width, config.Height)
	}
	if !bytes.Equal(got["2.png"], small) {
		t.Error("a page under the size was touched")
	}
	if want := (comicrepack.Result{Entries: 2, Pages: 2, Reencoded: 1, Downscaled: 1}); result != want {
		t.Errorf("result = %+v, want %+v", result, want)
	}
}

func TestEdgeFor(t *testing.T) {
	aim := comicrepack.Aim{PageLong: 3000, PageShort: 2000}
	for _, tc := range []struct{ width, height, want int }{
		{1000, 1500, 1500}, // fits: never upscaled
		{4000, 6000, 3000}, // long edge decides
		{3000, 6000, 3000}, // both edges over: the harder cap decides
		{6000, 4000, 3000}, // a landscape spread is measured the same way
		{1800, 2800, 2800}, // within both
	} {
		if got := comicrepack.EdgeFor(tc.width, tc.height, aim); got != tc.want {
			t.Errorf("EdgeFor(%d, %d) = %d, want %d", tc.width, tc.height, got, tc.want)
		}
	}
	if got := comicrepack.EdgeFor(5000, 5000, comicrepack.Aim{}); got != 5000 {
		t.Errorf("no aim: %d", got)
	}
}

func TestCanonicalFormat(t *testing.T) {
	if got := []string{comicrepack.CanonicalFormat(" JPEG"), comicrepack.CanonicalFormat(".WebP"), comicrepack.CanonicalFormat("")}; !reflect.DeepEqual(got, []string{"jpg", "webp", ""}) {
		t.Errorf("got %q", got)
	}
}

func TestMeasure_CountsPagesAndTakesTheMedianOfTheOnesThatAreNotSpreads(t *testing.T) {
	fsys := newFS()
	put(t, fsys, "/lib/a.cbt", tarOf(t, []member{
		{"1.png", noisyPNG(t, 40, 60)}, {"2.png", noisyPNG(t, 40, 60)}, {"3.png", noisyPNG(t, 50, 70)},
		{"4.png", noisyPNG(t, 120, 60)}, {"ComicInfo.xml", []byte("<ComicInfo/>")},
	}))
	got, err := comicrepack.Measure(context.Background(), fsys, "/lib/a.cbt")
	if err != nil {
		t.Fatal(err)
	}
	if want := (comicrepack.Pages{Count: 4, MedianLong: 60, MedianShort: 40, Format: "png"}); got != want {
		t.Errorf("pages = %+v, want %+v", got, want)
	}
}
