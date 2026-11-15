package adapters_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// An import never walks a library root, the folder above one, or Solarr's own workspace (API-2).
func TestImportInspector_RefusesSourcesThatWouldImportTheLibraryIntoItself(t *testing.T) {
	cache := newSweepCache(t)
	if _, err := cache.CreateRootFolder(t.Context(), "/media/music", "Music"); err != nil {
		t.Fatal(err)
	}
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	for _, directory := range []string{"/media/music", "/media", "/downloads/incoming", "/config/cache/work"} {
		if err := fsys.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inspector := adapters.ImportInspector{Settings: cache, FS: fsys, Workspace: "/config/cache"}
	for source, want := range map[string]error{
		"/media/music":        apiserver.ErrImportSourceRefused,
		"/media":              apiserver.ErrImportSourceRefused,
		"/config/cache/work":  apiserver.ErrImportSourceRefused,
		"/downloads/missing":  apiserver.ErrInvalidChoice,
		"/downloads/incoming": nil,
	} {
		if got := inspector.CheckSource(source); !errors.Is(got, want) && (want != nil || got != nil) {
			t.Errorf("CheckSource(%q) = %v, want %v", source, got, want)
		}
	}
}
