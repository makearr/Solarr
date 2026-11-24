package transcoder_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"path"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func comicTarget() settings.QualityTarget {
	return settings.QualityTarget{Name: "Comic", MediaType: "book", Container: "cbz", ImageFormat: "webp", ImageQuality: 70, PageLong: 3000, PageShort: 2000}
}

func TestAnalyzeComic(t *testing.T) {
	target := comicTarget()
	for _, tc := range []struct {
		name    string
		facts   transcoder.ComicFacts
		want    transcoder.Verdict
		reasons []transcoder.Reason
	}{
		{"a cbr is repacked", transcoder.ComicFacts{Container: "cbr", Pages: 20, ImageFormat: "webp"}, transcoder.Needed,
			[]transcoder.Reason{{Code: transcoder.ReasonContainer, Stream: -1, Have: "cbr", Want: "cbz"}}},
		{"pages in another format", transcoder.ComicFacts{Container: "cbz", Pages: 20, ImageFormat: "jpg"}, transcoder.Needed,
			[]transcoder.Reason{{Code: transcoder.ReasonImageFormat, Stream: -1, Have: "jpg", Want: "webp"}}},
		{"pages well over the size", transcoder.ComicFacts{Container: "cbz", Pages: 20, ImageFormat: "webp", MedianLong: 4000, MedianShort: 2600}, transcoder.Needed,
			[]transcoder.Reason{{Code: transcoder.ReasonPageSize, Stream: -1, Have: "4000x2600", Want: "3000x2000"}}},
		{"pages a little over the size are left", transcoder.ComicFacts{Container: "cbz", Pages: 20, ImageFormat: "webp", MedianLong: 3200, MedianShort: 2100}, transcoder.NotNeeded,
			[]transcoder.Reason{{Code: transcoder.ReasonNothingToDo, Stream: -1}}},
		{"a pdf is not a comic archive", transcoder.ComicFacts{Container: "pdf", Pages: 20}, transcoder.NotNeeded,
			[]transcoder.Reason{{Code: transcoder.ReasonUnsupportedContainer, Stream: -1, Have: "pdf"}}},
		{"an archive without pages", transcoder.ComicFacts{Container: "cbz"}, transcoder.Blocked,
			[]transcoder.Reason{{Code: transcoder.ReasonNoPages, Stream: -1}}},
	} {
		plan := transcoder.AnalyzeComic(tc.facts, target)
		if plan.Verdict != tc.want || !reflect.DeepEqual(plan.Reasons, tc.reasons) {
			t.Errorf("%s: plan = %v %+v, want %v %+v", tc.name, plan.Verdict, plan.Reasons, tc.want, tc.reasons)
		}
	}
}

func TestAnalyzeComic_ATargetThatNamesNoFormatOrSizeOnlyAsksForACbz(t *testing.T) {
	plan := transcoder.AnalyzeComic(transcoder.ComicFacts{Container: "cb7", Pages: 9, ImageFormat: "png", MedianLong: 9000}, settings.QualityTarget{Container: "cbz"})
	if plan.Verdict != transcoder.Needed || len(plan.Reasons) != 1 || plan.Reasons[0].Code != transcoder.ReasonContainer {
		t.Errorf("plan = %+v, want a repack for the container alone", plan)
	}
}

func TestAimOf_OnlyWhatThePlanNames(t *testing.T) {
	target := comicTarget()
	plan := transcoder.Plan{Reasons: []transcoder.Reason{{Code: transcoder.ReasonContainer}}}
	if aim := transcoder.AimOf(plan, target); aim.Format != "" || aim.PageLong != 0 {
		t.Errorf("aim = %+v, want pages left alone", aim)
	}
	plan.Reasons = append(plan.Reasons, transcoder.Reason{Code: transcoder.ReasonImageFormat}, transcoder.Reason{Code: transcoder.ReasonPageSize})
	if aim := transcoder.AimOf(plan, target); aim.Format != "webp" || aim.Quality != 70 || aim.PageLong != 3000 || aim.PageShort != 2000 {
		t.Errorf("aim = %+v, want the target's format, quality and page size", aim)
	}
}

type fakeComics struct {
	facts map[string]transcoder.ComicFacts
}

func (f fakeComics) ProbeComic(_ context.Context, absolute string) (transcoder.ComicFacts, error) {
	if found, ok := f.facts[absolute]; ok {
		return found, nil
	}
	return transcoder.ComicFacts{}, errors.New("unreadable archive")
}

func noisyPage(t *testing.T, width, height int) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(3))
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

func archiveOf(t *testing.T, tarball bool, pages map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if tarball {
		writer := tar.NewWriter(&buf)
		for name, body := range pages {
			_ = writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
			_, _ = writer.Write(body)
		}
		_ = writer.Close()
		return buf.Bytes()
	}
	writer := zip.NewWriter(&buf)
	for name, body := range pages {
		out, _ := writer.Create(name)
		_, _ = out.Write(body)
	}
	_ = writer.Close()
	return buf.Bytes()
}

// addComic puts a real archive in the library and judges nothing: the run reads it as the dispatcher would have left it.
func (p *pipeline) addComic(t *testing.T, id int64, rel string, body []byte, facts transcoder.ComicFacts) {
	t.Helper()
	absolute := path.Join("/lib", rel)
	if err := p.fs.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := p.fs.Create(absolute)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = out.Write(body)
	_ = out.Close()
	comics, _ := p.svc.Comics.(fakeComics)
	if comics.facts == nil {
		comics = fakeComics{facts: map[string]transcoder.ComicFacts{}}
	}
	comics.facts[absolute] = facts
	p.svc.Comics = comics
	p.items.items[id] = &library.TranscodeItem{MediaObjectID: id, CategoryID: 3, Kind: transcoder.KindComic, State: library.TranscodeRunning, TargetID: 9}
	p.items.paths[id] = []library.ObjectPath{{RootFolderID: 7, RelPath: rel}}
}

func comicPipeline(t *testing.T, target settings.QualityTarget) *pipeline {
	t.Helper()
	p := newPipeline(t, target, nil)
	p.settings.mediaType = "comic"
	return p
}

func zipNames(t *testing.T, p *pipeline, absolute string) []string {
	t.Helper()
	file, err := p.fs.Open(absolute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, member := range reader.File {
		names = append(names, member.Name)
	}
	return names
}

func TestRunFile_ARepackedTarComicReplacesTheOriginalThroughTheBinAsACbz(t *testing.T) {
	target := settings.QualityTarget{Name: "Comic", MediaType: "book", Container: "cbz"}
	p := comicPipeline(t, target)
	page := noisyPage(t, 60, 90)
	body := archiveOf(t, true, map[string][]byte{"10.png": page, "2.png": page, "1.png": page, "ComicInfo.xml": []byte("<ComicInfo/>")})
	p.addComic(t, 1, "Series/issue.cbt", body, transcoder.ComicFacts{Container: "cbt", Pages: 3, ImageFormat: "png"})
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeDone || item.Engine != "comic" || item.OriginalRecycleID != 1 {
		t.Errorf("item = %+v, want done by the comic engine with the original in bin entry 1", item)
	}
	if p.exists("/lib/Series/issue.cbt") || !p.exists("/lib/Series/issue.cbz") || !p.exists("/lib/recycle-bin/day/1-issue.cbt") {
		t.Errorf("cbt %v, cbz %v, bin copy %v; want only the cbz in place and the tar in the bin", p.exists("/lib/Series/issue.cbt"), p.exists("/lib/Series/issue.cbz"), p.exists("/lib/recycle-bin/day/1-issue.cbt"))
	}
	if got, want := zipNames(t, p, "/lib/Series/issue.cbz"), []string{"1.png", "2.png", "10.png", "ComicInfo.xml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pages = %q, want natural order with ComicInfo kept: %q", got, want)
	}
	if len(p.items.replaced) != 1 || p.items.replaced[0].OldRelPath != "Series/issue.cbt" || p.items.replaced[0].NewRelPath != "Series/issue.cbz" {
		t.Errorf("replaced = %+v, want the library moved to the cbz name", p.items.replaced)
	}
	if len(p.reported.done) != 1 || p.reported.done[0].Kind != transcoder.KindComic || p.reported.done[0].Engine != "comic" {
		t.Errorf("reported = %+v, want one comic conversion", p.reported.done)
	}
}

func TestRunFile_ReencodingToWebPShrinksTheFile(t *testing.T) {
	p := comicPipeline(t, comicTarget())
	page := noisyPage(t, 120, 180)
	body := archiveOf(t, false, map[string][]byte{"1.png": page, "2.png": page})
	p.addComic(t, 1, "a.cbz", body, transcoder.ComicFacts{Container: "cbz", Pages: 2, ImageFormat: "png"})
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeDone || item.ResultSizeBytes >= int64(len(body))*9/10 {
		t.Errorf("item = %+v, want done at under 90%% of %d bytes", item, len(body))
	}
	if got, want := zipNames(t, p, "/lib/a.cbz"), []string{"1.webp", "2.webp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pages = %q, want %q", got, want)
	}
}

func TestRunFile_APagesOnlyRepackThatSavesUnderTenPercentLeavesTheFileAlone(t *testing.T) {
	p := comicPipeline(t, comicTarget())
	page := noisyPage(t, 100, 100)
	body := archiveOf(t, false, map[string][]byte{"1.png": page, "2.png": page})
	// The probe claims big pages, the real ones already fit: nothing shrinks.
	p.addComic(t, 1, "a.cbz", body, transcoder.ComicFacts{Container: "cbz", Pages: 2, ImageFormat: "webp", MedianLong: 4000, MedianShort: 2600})
	p.svc.Settings.(*memSettings).target.ImageFormat = ""
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeNotNeeded || item.ReasonsJSON != `[{"code":"saves_too_little","stream":-1,"have":"100"}]` {
		t.Errorf("item = %+v, want not_needed because the repack saved nothing", item)
	}
	if !p.exists("/lib/a.cbz") || len(p.items.replaced) != 0 || len(p.bin.entries) != 0 {
		t.Error("the original was touched")
	}
}

func TestRunFile_AnUnreadableComicIsBlockedNotRetried(t *testing.T) {
	p := comicPipeline(t, comicTarget())
	p.addComic(t, 1, "a.cbz", []byte("not an archive"), transcoder.ComicFacts{})
	p.svc.Comics = fakeComics{facts: map[string]transcoder.ComicFacts{}}
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeBlocked {
		t.Errorf("item = %+v, want blocked", item)
	}
}

func TestAnalyze_JudgesComicsOfAComicsLibraryAndIgnoresOtherFiles(t *testing.T) {
	items := newMemItems()
	items.objects = []library.ObjectToAnalyze{
		{MediaObjectID: 1, Kind: "book", RootFolderID: 7, RelPath: "a.cbr"},
		{MediaObjectID: 2, Kind: "book", RootFolderID: 7, RelPath: "b.cbz"},
		{MediaObjectID: 3, Kind: "audio", RootFolderID: 7, RelPath: "c.flac"},
		{MediaObjectID: 4, Kind: "book", RootFolderID: 7, RelPath: "gone.cbz"},
	}
	svc := analyzeService(items, true, true)
	target := comicTarget()
	target.ID = 9
	svc.Settings = &memSettings{global: settings.TranscodeGlobal{Enabled: true}, target: target, enabled: true, mediaType: "comic"}
	svc.Comics = fakeComics{facts: map[string]transcoder.ComicFacts{
		"/lib/a.cbr": {Pages: 10, ImageFormat: "webp"}, "/lib/b.cbz": {Pages: 10, ImageFormat: "webp"},
	}}
	if err := svc.Analyze(t.Context(), 3, nil); err != nil {
		t.Fatal(err)
	}
	type verdict struct {
		ID           int64
		Kind, State  string
		Lane, Reason string
	}
	var got []verdict
	for _, item := range items.judged {
		got = append(got, verdict{item.MediaObjectID, item.Kind, string(item.State), item.Lane, item.ReasonsJSON})
	}
	want := []verdict{
		{1, "comic", "needed", "audio", `[{"code":"container","stream":-1,"want":"cbz","have":"cbr"}]`},
		{2, "comic", "not_needed", "", `[{"code":"nothing_to_do","stream":-1}]`},
		{4, "comic", "blocked", "", `[{"code":"source_unreadable","stream":-1,"have":"unreadable archive"}]`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("judged = %+v, want %+v", got, want)
	}
}

func TestAnalyze_AVideoLibraryNeverJudgesABook(t *testing.T) {
	items := newMemItems()
	items.objects = []library.ObjectToAnalyze{{MediaObjectID: 1, Kind: "book", RootFolderID: 7, RelPath: "a.cbz"}}
	if err := analyzeService(items, true, true).Analyze(t.Context(), 3, nil); err != nil {
		t.Fatal(err)
	}
	if len(items.judged) != 0 {
		t.Errorf("judged = %+v, want none", items.judged)
	}
}

func TestWorthKeeping(t *testing.T) {
	container := transcoder.Plan{Reasons: []transcoder.Reason{{Code: transcoder.ReasonContainer}}}
	pages := transcoder.Plan{Reasons: []transcoder.Reason{{Code: transcoder.ReasonPageSize}}}
	both := transcoder.Plan{Reasons: []transcoder.Reason{{Code: transcoder.ReasonContainer}, {Code: transcoder.ReasonImageFormat}}}
	for _, tc := range []struct {
		name   string
		plan   transcoder.Plan
		result int64
		want   bool
	}{
		{"a new container is kept even when larger", container, 1200, true},
		{"a new container with new pages is kept even when larger", both, 1200, true},
		{"pages saving exactly ten percent", pages, 900, true},
		{"pages saving under ten percent", pages, 901, false},
		{"pages saving nothing", pages, 1000, false},
		{"pages that grew", pages, 1100, false},
	} {
		if got := transcoder.WorthKeeping(tc.plan, 1000, tc.result); got != tc.want {
			t.Errorf("%s: WorthKeeping = %v, want %v", tc.name, got, tc.want)
		}
	}
}
