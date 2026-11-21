package importjob

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type releaseReadingImporter struct{ importbatch.Importer }

func (releaseReadingImporter) ReleaseFactsOf(title string) (importbatch.ReleaseFacts, bool) {
	if title == "Show.S01E01.1080p.WEB-DL.x264-GRP" || title == "Show.S01.REPACK.1080p.BluRay-GRP" {
		return importbatch.ReleaseFacts{Source: "webdl", Group: "GRP", Revision: 2}, true
	}
	return importbatch.ReleaseFacts{}, false
}

type acquisitionsByID map[int64]library.Acquisition

func (a acquisitionsByID) Get(_ context.Context, id int64) (library.Acquisition, error) {
	found, ok := a[id]
	if !ok {
		return library.Acquisition{}, errors.New("no such download")
	}
	return found, nil
}

// An import keeps what the grabbed release said, or what a release-named folder says; anything else keeps nothing (QUAL-1).
func TestReleaseNotes_OriginOf(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	indexer := int64(4)
	notes := releaseNotes{
		clock: fake,
		acquisitions: acquisitionsByID{
			1: {ID: 1, CandidateTitle: "Show.S01E01.1080p.WEB-DL.x264-GRP", IndexerID: &indexer, CreatedAt: 500},
			2: {ID: 2, CandidateTitle: "an obfuscated name"},
		},
	}
	one, none := int64(1), int64(2)
	reader := releaseReadingImporter{}
	want := &library.ReleaseOrigin{Title: "Show.S01E01.1080p.WEB-DL.x264-GRP", Source: "webdl", Group: "GRP", Revision: 2, IndexerID: &indexer, GrabbedAt: 500}
	got := notes.originOf(t.Context(), reader, &one, "/downloads/whatever")
	if got == nil || *got.IndexerID != indexer || got.Title != want.Title || got.Source != "webdl" || got.Group != "GRP" || got.Revision != 2 || got.GrabbedAt != 500 {
		t.Errorf("a download = %+v, want %+v", got, want)
	}
	manual := notes.originOf(t.Context(), reader, nil, "/incoming/Show.S01.REPACK.1080p.BluRay-GRP")
	if manual == nil || manual.Title != "Show.S01.REPACK.1080p.BluRay-GRP" || manual.IndexerID != nil || manual.GrabbedAt != fake.Now().Unix() {
		t.Errorf("a release-named folder = %+v, want its name kept, no indexer, grabbed now (%v)", manual, time.Unix(fake.Now().Unix(), 0))
	}
	if notes.originOf(t.Context(), reader, nil, "/incoming/My Shows") != nil {
		t.Error("an ordinary folder kept a release")
	}
	if notes.originOf(t.Context(), reader, &none, "/downloads/x") != nil {
		t.Error("a name the media type cannot read kept a release")
	}
	if notes.originOf(t.Context(), struct{ importbatch.Importer }{}, &one, "/downloads/x") != nil {
		t.Error("a media type without a reader kept a release")
	}
	if (releaseNotes{}).originOf(t.Context(), reader, &one, "/downloads/x") != nil {
		t.Error("the zero value kept a release")
	}
}
