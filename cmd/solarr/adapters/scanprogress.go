package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ScanProgress implements apiserver.ScanReader from scan_state: the scan's own running count against the last scan's
// total, never a walk of its own, which on a large tree takes longer than the scan.
type ScanProgress struct {
	Scans library.Scans
	Cache *settings.Cache
}

var _ apiserver.ScanReader = ScanProgress{}

// RunningScans implements apiserver.ScanReader.
func (a ScanProgress) RunningScans(ctx context.Context) ([]gen.ScanRun, error) {
	states, err := a.Scans.All(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := a.Cache.Snapshot()
	libraries := map[int64]string{}
	for _, category := range snapshot.Categories() {
		for _, root := range snapshot.RootFoldersForCategory(category.ID) {
			libraries[root.ID] = category.Name
		}
	}
	scans := []gen.ScanRun{}
	for _, state := range states {
		root, found := snapshot.RootFolder(state.RootFolderID)
		if !found || !state.Running() {
			continue
		}
		run := gen.ScanRun{
			RootFolderId: root.ID, Library: libraries[root.ID], Path: root.Path, Full: state.RunFull,
			FilesSeen: state.FilesSeen, FilesExpected: state.FilesFound,
		}
		if !state.RunStartedAt.IsZero() {
			run.StartedAt = &state.RunStartedAt
		}
		scans = append(scans, run)
	}
	return scans, nil
}
