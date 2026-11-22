package music_test

import (
	"context"
	"errors"
	"github.com/makearr/Solarr/internal/platform/clock"
	"image"
	"io"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// fakeTagFiles records every edit and answers each key it was given as changed.
type fakeTagFiles struct {
	mediatool.Tool
	fs         fsx.FS
	refuse     map[string]bool
	failBatch  error
	edits      []mediatool.TagEdit
	coverSeen  bool
	coverFound bool
}

func (f *fakeTagFiles) WriteTags(_ context.Context, edits []mediatool.TagEdit) ([]mediatool.TagResult, error) {
	if f.failBatch != nil {
		return nil, f.failBatch
	}
	results := make([]mediatool.TagResult, len(edits))
	for i, edit := range edits {
		if f.refuse[edit.Path] {
			results[i].Err = errors.New("read-only file")
			continue
		}
		if edit.Cover != "" {
			f.coverSeen = true
			_, err := f.fs.Stat(edit.Cover)
			f.coverFound = err == nil
		}
		f.edits = append(f.edits, edit)
		for key := range edit.Fill {
			results[i].Changed = append(results[i].Changed, key)
		}
		if _, err := f.fs.Lstat(edit.Path); err == nil {
			_ = fsx.WriteAtomic(f.fs, edit.Path, func(w io.Writer) error { _, err := w.Write([]byte("tagged")); return err })
		}
	}
	return results, nil
}

// identifyFile gives one file a work with a release group and an edition with a release.
func identifyFile(t *testing.T, opened *store.Store, relPath, releaseGroup, release string) int64 {
	t.Helper()
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: "hash-" + relPath, FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	resolved, err := library.NewIdentity(opened).ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroup, Confidence: 1,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: relPath, SortTitle: relPath},
		RootFolderID: 1, MediaObjectID: registered[0].MediaObjectID,
		Edition: &library.EditionLink{
			Provider: music.ProviderRelease, ExternalID: release, Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: release},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveWorksDetailed: %v", err)
	}
	return resolved[0].WorkID
}

type tagWritebackFixture struct {
	job    *music.TagWritebackJob
	opened *store.Store
	tool   *fakeTagFiles
	fs     *fsx.Mem
	roots  []music.TagRoot
	clock  clock.Clock
}

func newTagWritebackJob(t *testing.T, roots ...music.TagRoot) *tagWritebackFixture {
	t.Helper()
	opened, fs, fake := newTestLibrary(t)
	fixture := &tagWritebackFixture{opened: opened, tool: &fakeTagFiles{fs: fs}, fs: fs, roots: roots, clock: fake}
	fixture.job = music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(opened),
		Roots: func(context.Context) ([]music.TagRoot, error) { return fixture.roots, nil },
		Tool:  fixture.tool, FS: fs, ImagesDir: "/cache/images", CoversDir: "/cache/tag-covers", Clock: fake,
	})
	return fixture
}

func (f *tagWritebackFixture) run(t *testing.T) []mediatool.TagEdit {
	t.Helper()
	f.tool.edits = nil
	if err := f.job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return f.tool.edits
}

var idsOnly = music.TagOptions{WriteIDs: true}

// Revision D3: the ids go in as fill -- the helper keeps a value the file already carries -- and a
// file is not sent again until what it should carry changes.
func TestTagWriteback_FillsIdsOnceUntilSomethingChanges(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: idsOnly})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")

	want := []mediatool.TagEdit{{
		Path: "/music/Heka/Zanuvode/01.m4a",
		Fill: mediatool.Tags{"musicbrainz_releasegroupid": "rg-1", "musicbrainz_albumid": "rel-1"},
		Set:  mediatool.Tags{},
	}}
	if edits := fixture.run(t); !reflect.DeepEqual(edits, want) {
		t.Errorf("edits =\n%v\nwant\n%v", edits, want)
	}
	if edits := fixture.run(t); len(edits) != 0 {
		t.Errorf("second run wrote %v, want nothing", edits)
	}

	// Replacing ids is a different request, so the file is sent again with the ids as set.
	fixture.roots[0].Options.ReplaceIDs = true
	want[0].Set, want[0].Fill = want[0].Fill, mediatool.Tags{}
	if edits := fixture.run(t); !reflect.DeepEqual(edits, want) {
		t.Errorf("after replace was switched on, edits =\n%v\nwant\n%v", edits, want)
	}
}

// A file rewritten in place has new bytes; the object follows, or a later move would refuse the copy for its size.
func TestTagWriteback_TheObjectFollowsTheRewrittenFile(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: idsOnly})
	const name = "/music/Heka/Zanuvode/01.m4a"
	if err := fixture.fs.MkdirAll("/music/Heka/Zanuvode", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fixture.fs, name, func(w io.Writer) error { _, err := w.Write([]byte("x")); return err }); err != nil {
		t.Fatal(err)
	}
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	fixture.run(t)

	wantQuick, err := library.QuickHash(fixture.fs, name, int64(len("tagged")))
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	var quick string
	if err := fixture.opened.Read().QueryRowContext(t.Context(), "SELECT size_bytes, quick_hash FROM media_object").Scan(&size, &quick); err != nil {
		t.Fatal(err)
	}
	if size != int64(len("tagged")) || quick != wantQuick {
		t.Errorf("object = %d bytes, hash %q; want %d bytes, hash %q", size, quick, len("tagged"), wantQuick)
	}
}

// "Write other tags": the providers' album and date are set, overwriting what the file says.
func TestTagWriteback_WritesMetadataWhenAsked(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: music.TagOptions{WriteMetadata: true}})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")

	want := []mediatool.TagEdit{{
		Path: "/music/Heka/Zanuvode/01.m4a", Fill: mediatool.Tags{},
		Set: mediatool.Tags{"album": "Heka/Zanuvode/01.m4a"},
	}}
	if edits := fixture.run(t); !reflect.DeepEqual(edits, want) {
		t.Errorf("edits =\n%v\nwant\n%v", edits, want)
	}
}

// "Embed the cover art if some was fetched": the cached WebP goes to the helper as a JPEG, which
// exists while the helper runs and is gone afterwards.
func TestTagWriteback_EmbedsAFetchedCover(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: music.TagOptions{EmbedCover: true}})
	workID := identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	putFile(t, fixture.fs, "/music/Heka/Zanuvode/01.m4a", "audio")
	if edits := fixture.run(t); len(edits) != 0 {
		t.Fatalf("with no cover fetched, wrote %v", edits)
	}

	encoded, err := artwork.EncodeWebP(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	if err := fixture.fs.MkdirAll("/cache/images", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fixture.fs, "/cache/images/work-1-cover-full.webp", func(w io.Writer) error {
		_, err := w.Write(encoded)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := library.NewArtworks(fixture.opened).Record(t.Context(), workID, "", "work-1-cover", 8, 8, 5); err != nil {
		t.Fatalf("Record: %v", err)
	}

	want := []mediatool.TagEdit{{
		Path: "/music/Heka/Zanuvode/01.m4a.solarr-tag", Fill: mediatool.Tags{}, Set: mediatool.Tags{},
		Cover: "/cache/tag-covers/work-1-cover.jpg",
	}}
	if edits := fixture.run(t); !reflect.DeepEqual(edits, want) {
		t.Errorf("edits =\n%v\nwant\n%v", edits, want)
	}
	if !fixture.tool.coverSeen || !fixture.tool.coverFound {
		t.Errorf("cover seen %v, present while writing %v; want both", fixture.tool.coverSeen, fixture.tool.coverFound)
	}
	if _, err := fixture.fs.Stat("/cache/tag-covers/work-1-cover.jpg"); err == nil {
		t.Error("the JPEG copy outlived the run")
	}
}

// Only the roots it is given: a read-only root, a library with tagging off or a download folder is
// never in the list.
func TestTagWriteback_LeavesOtherRootsAlone(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 2, Path: "/elsewhere", Options: idsOnly})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	if edits := fixture.run(t); len(edits) != 0 {
		t.Errorf("wrote %v into a root it was not given", edits)
	}
}

// A library with every option off is not written at all.
func TestTagWriteback_NothingAskedNothingWritten(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music"})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	if edits := fixture.run(t); len(edits) != 0 {
		t.Errorf("wrote %v with every option off", edits)
	}
}

// Navidrome's profile (D10): the track artist's tags are filled, and the album's cover is put in its folder as
// cover.jpg once, never over a cover file the folder already has.
func TestTagWriteback_NavidromeProfileFillsArtistTagsAndWritesCoverJpg(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: music.TagOptions{Navidrome: true}})
	workID := identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	identifyFile(t, fixture.opened, "Heka/Zanuvode/02.m4a", "rg-1", "rel-1")

	encoded, err := artwork.EncodeWebP(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.fs.MkdirAll("/cache/images", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fixture.fs, "/cache/images/work-1-cover-full.webp", func(w io.Writer) error {
		_, err := w.Write(encoded)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := library.NewArtworks(fixture.opened).Record(t.Context(), workID, "", "work-1-cover", 8, 8, 5); err != nil {
		t.Fatal(err)
	}

	if err := fixture.fs.MkdirAll("/music/Heka/Zanuvode", 0o755); err != nil {
		t.Fatal(err)
	}
	fixture.run(t)
	info, err := fixture.fs.Stat("/music/Heka/Zanuvode/cover.jpg")
	if err != nil || info.Size == 0 {
		t.Fatalf("cover.jpg = %+v, %v; want it written beside the tracks", info, err)
	}

	// A folder that already has a cover keeps it.
	if err := fixture.fs.Remove("/music/Heka/Zanuvode/cover.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fixture.fs, "/music/Heka/Zanuvode/folder.jpg", func(w io.Writer) error { _, err := w.Write([]byte("mine")); return err }); err != nil {
		t.Fatal(err)
	}
	fixture.roots[0].Options.WriteIDs = true // a changed request, so the files are visited again
	fixture.run(t)
	if _, err := fixture.fs.Stat("/music/Heka/Zanuvode/cover.jpg"); err == nil {
		t.Error("cover.jpg was written into a folder that already had folder.jpg")
	}
}

// A page the helper cannot take is logged and counted; the run goes on to the next page instead of failing for good.
func TestTagWriteback_APageTheHelperRefusesDoesNotFailTheRun(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: idsOnly})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	fixture.tool.failBatch = errors.New("argument list too long")

	if err := fixture.job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Errorf("Run = %v, want the failed page logged and the run finished", err)
	}
}

// A helper that is not installed fails the run: every later page would fail the same way.
func TestTagWriteback_AMissingHelperStopsTheRun(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: idsOnly})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	fixture.tool.failBatch = mediatool.ErrToolMissing

	if err := fixture.job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); !errors.Is(err, mediatool.ErrToolMissing) {
		t.Errorf("Run = %v, want ErrToolMissing", err)
	}
}

// putFile creates a library file with content.
func putFile(t *testing.T, fs fsx.FS, name, content string) {
	t.Helper()
	if err := fs.MkdirAll(path.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(fs, name, func(w io.Writer) error { _, err := w.Write([]byte(content)); return err }); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, fs fsx.FS, name string) string {
	t.Helper()
	file, err := fs.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// A file a download links to is the download's file: by default it waits, untouched, until the link is gone.
func TestTagWriteback_ALinkedFileWaitsByDefault(t *testing.T) {
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: idsOnly})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	putFile(t, fixture.fs, "/music/Heka/Zanuvode/01.m4a", "audio")
	if err := fixture.fs.MkdirAll("/downloads", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.fs.Link("/music/Heka/Zanuvode/01.m4a", "/downloads/01.m4a"); err != nil {
		t.Fatal(err)
	}

	if edits := fixture.run(t); len(edits) != 0 {
		t.Fatalf("a linked file was sent to the helper: %v", edits)
	}
	if err := fixture.fs.Remove("/downloads/01.m4a"); err != nil {
		t.Fatal(err)
	}
	if edits := fixture.run(t); len(edits) != 1 {
		t.Errorf("once the link was gone, edits = %v, want the file written", edits)
	}
}

// With the copy option the library name gets a tagged copy and the download keeps the original bytes.
func TestTagWriteback_ALinkedFileIsTaggedOnACopyWhenAsked(t *testing.T) {
	options := music.TagOptions{WriteIDs: true, CopyLinkedFiles: true}
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: options})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	putFile(t, fixture.fs, "/music/Heka/Zanuvode/01.m4a", "audio")
	if err := fixture.fs.MkdirAll("/downloads", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.fs.Link("/music/Heka/Zanuvode/01.m4a", "/downloads/01.m4a"); err != nil {
		t.Fatal(err)
	}

	fixture.run(t)
	if got := readFile(t, fixture.fs, "/music/Heka/Zanuvode/01.m4a"); got != "tagged" {
		t.Errorf("library file = %q, want the tagged copy", got)
	}
	if got := readFile(t, fixture.fs, "/downloads/01.m4a"); got != "audio" {
		t.Errorf("download file = %q, want the untouched original", got)
	}
	if _, err := fixture.fs.Lstat("/music/Heka/Zanuvode/01.m4a.solarr-tag"); err == nil {
		t.Error("the private copy was left behind")
	}
}

// orderSpy records the order of the calls that decide whether a swapped-in file survives a crash.
type orderSpy struct {
	fsx.FS
	calls []string
}

func (o *orderSpy) Sync(path string) error {
	o.calls = append(o.calls, "sync "+path)
	return o.FS.Sync(path)
}

func (o *orderSpy) Rename(oldPath, newPath string) error {
	o.calls = append(o.calls, "rename "+newPath)
	return o.FS.Rename(oldPath, newPath)
}

// TAG-1: the tagged copy is flushed before it replaces the file.
func TestTagWriteback_ATaggedCopyIsFlushedBeforeItReplacesTheFile(t *testing.T) {
	options := music.TagOptions{WriteIDs: true, CopyLinkedFiles: true}
	fixture := newTagWritebackJob(t, music.TagRoot{ID: 1, Path: "/music", Options: options})
	identifyFile(t, fixture.opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	putFile(t, fixture.fs, "/music/Heka/Zanuvode/01.m4a", "audio")
	if err := fixture.fs.MkdirAll("/downloads", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fixture.fs.Link("/music/Heka/Zanuvode/01.m4a", "/downloads/01.m4a"); err != nil {
		t.Fatal(err)
	}
	spy := &orderSpy{FS: fixture.fs}
	job := music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(fixture.opened),
		Roots: func(context.Context) ([]music.TagRoot, error) { return fixture.roots, nil },
		Tool:  fixture.tool, FS: spy, ImagesDir: "/cache/images", CoversDir: "/cache/tag-covers", Clock: fixture.clock,
	})
	if err := job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"sync /music/Heka/Zanuvode/01.m4a.solarr-tag", "rename /music/Heka/Zanuvode/01.m4a"}
	var swap []string
	for _, call := range spy.calls {
		if strings.HasPrefix(call, "sync /music") || call == "rename /music/Heka/Zanuvode/01.m4a" {
			swap = append(swap, call)
		}
	}
	if !reflect.DeepEqual(swap, want) {
		t.Fatalf("calls = %v, want %v", swap, want)
	}
}
