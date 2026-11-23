package importjob_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// packingImporter is a type whose downloads may be folders of loose pages: it packs each source into one file and
// places every unit it is handed.
type packingImporter struct {
	fsys   *fsx.Mem
	t      *testing.T
	workID int64
	asked  []string
	handed []string
}

func (*packingImporter) TakesBookFiles() bool { return true }

func (p *packingImporter) PackFolders(_ context.Context, source, workspace string) ([]batch.PackedFolder, error) {
	p.asked = append(p.asked, source)
	target := filepath.Join(workspace, "Invented Hero 001.cbz")
	writeFile(p.t, p.fsys, target, 200*1024)
	return []batch.PackedFolder{{File: target, Pages: []string{source + "/01.jpg", source + "/02.jpg"}}}, nil
}

func (p *packingImporter) ImportBatch(_ context.Context, request batch.Request) (batch.Result, error) {
	result := batch.Result{Placements: make([]*library.Placement, len(request.Units))}
	for index, unit := range request.Units {
		for _, file := range unit.Media {
			p.handed = append(p.handed, file.RelPath)
		}
		result.Decisions = append(result.Decisions, library.UnitDecision{UnitID: request.UnitIDs[index], Decision: library.UnitCommit, Confidence: 1})
		result.Placements[index] = &library.Placement{WorkID: p.workID}
	}
	return result, nil
}

// A folder of loose pages is packed only for a library whose type packs, imported from the workspace, and its pages go to
// the recycle bin only once a hand import placed the packed file; a download's own folder is never touched.
func TestJob_PacksAFolderOfPagesOnlyForALibraryWhoseTypePacks(t *testing.T) {
	catID := int64(7)
	cases := []struct {
		name         string
		payload      importjob.Payload
		wantAsked    []string
		wantHanded   []string
		wantRecycled []string
	}{
		{
			name:         "manual import into the library",
			payload:      importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual, CategoryID: &catID},
			wantAsked:    []string{root},
			wantHanded:   []string{"Invented Hero 001.cbz"},
			wantRecycled: []string{root + "/01.jpg", root + "/02.jpg"},
		},
		{
			name:       "a download keeps its folder",
			payload:    importjob.Payload{SourcePath: root, Origin: library.ImportOriginAcquisition, CategoryID: &catID},
			wantAsked:  []string{root},
			wantHanded: []string{"Invented Hero 001.cbz"},
		},
		{
			name:    "best match packs nothing",
			payload: importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual, BestMatch: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t, "01.jpg", "02.jpg")
			own := &packingImporter{fsys: fx.fsys, t: t, workID: seedWork(t, fx)}
			var recycled []string
			job := importjob.NewJob(importjob.Config{
				FS: fx.fsys, Sessions: fx.sessions(), Policy: fixedPolicy(0.9),
				Importers: map[string]batch.Importer{"pages": own}, MediaTypeOf: func(int64) string { return "pages" }, Unassigned: "pages",
				Libraries: func() []importjob.Library { return []importjob.Library{{CategoryID: catID, MediaType: "pages"}} },
				Packs:     library.NewPacks(fx.store), Workspace: "/workspace", ArchiveLimits: testArchiveLimits(),
				RecycleArchive: func(_ context.Context, _ *int64, _, file string) error {
					recycled = append(recycled, file)
					return nil
				},
				Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
			})
			if err := fx.run(t, job, tc.payload); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !reflect.DeepEqual(own.asked, tc.wantAsked) || !reflect.DeepEqual(own.handed, tc.wantHanded) || !reflect.DeepEqual(recycled, tc.wantRecycled) {
				t.Errorf("asked %v, handed %v, recycled %v; want %v, %v, %v", own.asked, own.handed, recycled, tc.wantAsked, tc.wantHanded, tc.wantRecycled)
			}
			if tc.wantHanded == nil {
				return
			}
			done, err := fx.sessions().List(t.Context(), library.ImportDone, false)
			if err != nil || len(done) != 1 || !strings.HasPrefix(done[0].SourcePath, "/workspace/") {
				t.Errorf("sessions = %+v, %v; want one over the workspace", done, err)
			}
			for _, page := range []string{"01.jpg", "02.jpg"} {
				if _, err := fx.fsys.Stat(filepath.Join(root, page)); err != nil {
					t.Errorf("source page %s: %v", page, err)
				}
			}
		})
	}
}
