package music_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// fakeTagFiles answers canned tags per path and records every edit.
type fakeTagFiles struct {
	mediatool.Tool
	tags   map[string]mediatool.Tags
	refuse map[string]bool
	edits  []mediatool.TagEdit
}

func (f *fakeTagFiles) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for i, path := range paths {
		probes[i] = mediatool.Probe{Path: path, Tags: f.tags[path]}
	}
	return probes, nil
}

func (f *fakeTagFiles) WriteTags(_ context.Context, edits []mediatool.TagEdit) ([]error, error) {
	failures := make([]error, len(edits))
	for i, edit := range edits {
		if f.refuse[edit.Path] {
			failures[i] = errors.New("read-only file")
			continue
		}
		f.edits = append(f.edits, edit)
	}
	return failures, nil
}

// identifyFile gives one file a work with a release group and an edition with a release.
func identifyFile(t *testing.T, opened *store.Store, relPath, releaseGroup, release string) {
	t.Helper()
	registered, err := library.NewPhysical(opened).RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: relPath, SizeBytes: 1, QuickHash: "hash-" + relPath, FirstSeenAt: firstSeen},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	if _, err := library.NewIdentity(opened).ResolveWorksDetailed(t.Context(), firstSeen, []library.WorkLink{{
		Provider: music.ProviderReleaseGroup, ExternalID: releaseGroup, Confidence: 1,
		Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: relPath, SortTitle: relPath},
		RootFolderID: 1, MediaObjectID: registered[0].MediaObjectID,
		Edition: &library.EditionLink{
			Provider: music.ProviderRelease, ExternalID: release, Confidence: 1,
			Edition: library.EditionIdentity{Kind: "unknown", Label: release},
		},
	}}); err != nil {
		t.Fatalf("ResolveWorksDetailed: %v", err)
	}
}

func newTagWritebackJob(t *testing.T, tool mediatool.Tool, roots []music.TagRoot) (*music.TagWritebackJob, *store.Store) {
	t.Helper()
	opened, _, fake := newTestLibrary(t)
	return music.NewTagWritebackJob(music.TagWritebackConfig{
		Files: library.NewTagWriteback(opened),
		Roots: func(context.Context) ([]music.TagRoot, error) { return roots, nil },
		Tool:  tool, Clock: fake,
	}), opened
}

// Revision D3: the ids are filled in where the file lacks them, and a value already there is
// never replaced -- even a different one.
func TestTagWriteback_FillsOnlyWhatTheFileLacks(t *testing.T) {
	tool := &fakeTagFiles{tags: map[string]mediatool.Tags{
		"/music/Heka/Zanuvode/01.m4a": {"MusicBrainz Album Id": "rel-from-picard"},
		"/music/Heka/Zanuvode/02.m4a": {},
	}}
	job, opened := newTagWritebackJob(t, tool, []music.TagRoot{{ID: 1, Path: "/music"}})
	identifyFile(t, opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")
	identifyFile(t, opened, "Heka/Zanuvode/02.m4a", "rg-2", "rel-2")

	if err := job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []mediatool.TagEdit{
		{Path: "/music/Heka/Zanuvode/01.m4a", Set: mediatool.Tags{"musicbrainz_releasegroupid": "rg-1"}},
		{Path: "/music/Heka/Zanuvode/02.m4a", Set: mediatool.Tags{"musicbrainz_releasegroupid": "rg-2", "musicbrainz_albumid": "rel-2"}},
	}
	if !reflect.DeepEqual(tool.edits, want) {
		t.Errorf("edits =\n%v\nwant\n%v", tool.edits, want)
	}

	// Once is enough: the next run finds nothing left to do, even a file whose write failed.
	tool.edits = nil
	if err := job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(tool.edits) != 0 {
		t.Errorf("second run wrote %v, want nothing", tool.edits)
	}
}

// Only the roots it is given: a read-only root or a library with write-back off is never in the list.
func TestTagWriteback_LeavesOtherRootsAlone(t *testing.T) {
	tool := &fakeTagFiles{tags: map[string]mediatool.Tags{}}
	job, opened := newTagWritebackJob(t, tool, []music.TagRoot{{ID: 2, Path: "/elsewhere"}})
	identifyFile(t, opened, "Heka/Zanuvode/01.m4a", "rg-1", "rel-1")

	if err := job.Run(t.Context(), jobs.Job{Type: music.TagWritebackJobType}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(tool.edits) != 0 {
		t.Errorf("wrote %v into a root it was not given", tool.edits)
	}
}
