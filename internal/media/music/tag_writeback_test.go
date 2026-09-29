package music_test

import (
	"context"
	"errors"
	"image"
	"io"
	"reflect"
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
	edits      []mediatool.TagEdit
	coverSeen  bool
	coverFound bool
}

func (f *fakeTagFiles) WriteTags(_ context.Context, edits []mediatool.TagEdit) ([]mediatool.TagResult, error) {
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
}

func newTagWritebackJob(t *testing.T, roots ...music.TagRoot) *tagWritebackFixture {
	t.Helper()
	opened, fs, fake := newTestLibrary(t)
	fixture := &tagWritebackFixture{opened: opened, tool: &fakeTagFiles{fs: fs}, fs: fs, roots: roots}
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
		Path: "/music/Heka/Zanuvode/01.m4a", Fill: mediatool.Tags{}, Set: mediatool.Tags{},
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
