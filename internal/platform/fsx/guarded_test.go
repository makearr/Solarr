package fsx_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestGuarded_RefusesWritesTheDenyFuncNames(t *testing.T) {
	inner := fsx.NewMem(clock.NewFakeAtEpoch())
	guarded := fsx.NewGuarded(inner, func(path string) error {
		if path == "/lib/a" || path == "/lib/b" {
			return fsx.ErrReadOnly
		}
		return nil
	})
	if _, err := guarded.Create("/lib/a"); !errors.Is(err, fsx.ErrReadOnly) {
		t.Errorf("Create = %v, want ErrReadOnly", err)
	}
	if err := guarded.Rename("/tmp/x", "/lib/b"); !errors.Is(err, fsx.ErrReadOnly) {
		t.Errorf("Rename into the library = %v, want ErrReadOnly", err)
	}
	if err := guarded.MkdirAll("/tmp", 0o755); err != nil {
		t.Errorf("MkdirAll outside = %v, want nil", err)
	}
	file, err := guarded.Create("/tmp/ok")
	if err != nil {
		t.Fatalf("Create outside = %v, want nil", err)
	}
	_ = file.Close()
	if _, statErr := inner.Stat("/lib/a"); statErr == nil {
		t.Error("the refused Create still made the file")
	}
}
