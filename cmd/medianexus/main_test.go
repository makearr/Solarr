package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRun_VersionReportsTheInjectedBuildMetadata(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"version"}, &out, io.Discard); err != nil {
		t.Fatalf("run(version) = %v", err)
	}

	for _, want := range []string{version, commit, buildDate} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("version output %q does not contain %q", out.String(), want)
		}
	}
}

func TestRun_UnknownCommandIsAnError(t *testing.T) {
	var out strings.Builder
	err := run([]string{"nonsense"}, &out, io.Discard)
	if err == nil {
		t.Fatal("run(nonsense) = nil, want an error")
	}
	if out.Len() != 0 {
		t.Errorf("unknown command wrote %q to stdout, want nothing", out.String())
	}
}

func TestRun_DefaultsToServe(t *testing.T) {
	var out strings.Builder
	if err := run(nil, &out, io.Discard); !errors.Is(err, errNotImplemented) {
		t.Errorf("run(nil) = %v, want errNotImplemented", err)
	}
}
