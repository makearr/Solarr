package apiserver_test

import (
	"errors"
	"io"
	"syscall"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

type readOnlyMount struct{ *fsx.Mem }

func (readOnlyMount) Create(string) (io.WriteCloser, error) { return nil, syscall.EROFS }

// A root on a read-only mount is reported as such, not as a permissions problem to chown away.
func TestFilesystemRoots_ReadOnlyMountIsNamed(t *testing.T) {
	mem := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := mem.MkdirAll("/media/x", 0o755); err != nil {
		t.Fatal(err)
	}
	err := apiserver.FilesystemRoots{FS: readOnlyMount{mem}}.Writable("/media/x")
	if !errors.Is(err, apiserver.ErrReadOnlyFilesystem) {
		t.Fatalf("err = %v, want ErrReadOnlyFilesystem", err)
	}
}
