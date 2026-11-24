package music

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// askedTool answers every path with its own base name as the album and records each path it is asked for.
type askedTool struct {
	mediatool.Tool
	asked []string
}

func (a *askedTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	a.asked = append(a.asked, paths...)
	probes := make([]mediatool.Probe, len(paths))
	for index, one := range paths {
		probes[index] = mediatool.Probe{Path: one, Tags: mediatool.Tags{"album": "Album of " + one}}
	}
	return probes, nil
}

func TestImporter_AFileTheInspectionProbedIsNotProbedAgainByTheBatch(t *testing.T) {
	tool := &askedTool{}
	importer := NewImporter(ImporterConfig{Tool: tool})
	if _, err := importer.InspectTags(t.Context(), "/in", []string{"a.flac", "b.flac"}); err != nil {
		t.Fatalf("InspectTags: %v", err)
	}
	probes, err := importer.probesFor(t.Context(), []string{"/in/a.flac", "/in/b.flac", "/in/c.flac"})
	if err != nil {
		t.Fatalf("probesFor: %v", err)
	}
	if want := []string{"/in/a.flac", "/in/b.flac", "/in/c.flac"}; len(tool.asked) != 3 || tool.asked[2] != want[2] {
		t.Fatalf("tool asked %v, want each of %v once, the third only by the batch", tool.asked, want)
	}
	for index, one := range []string{"/in/a.flac", "/in/b.flac", "/in/c.flac"} {
		if probes[index].Path != one {
			t.Errorf("probe %d is for %q, want %q", index, probes[index].Path, one)
		}
	}
	if _, err := importer.probesFor(t.Context(), []string{"/in/a.flac"}); err != nil || len(tool.asked) != 4 {
		t.Fatalf("tool asked %v; a probe is handed over once, so a second batch reads the file again", tool.asked)
	}
}

type failingTool struct{ mediatool.Tool }

func (failingTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for index, one := range paths {
		probes[index] = mediatool.Probe{Path: one, Err: errors.New("not audio")}
	}
	return probes, nil
}

func TestImporter_InspectTagsLogsHowManyFilesCouldNotBeRead(t *testing.T) {
	var out bytes.Buffer
	importer := NewImporter(ImporterConfig{Tool: failingTool{}, Logger: slog.New(slog.NewTextHandler(&out, nil))})
	if _, err := importer.InspectTags(t.Context(), "/in", []string{"a.flac", "b.flac"}); err != nil {
		t.Fatalf("InspectTags: %v", err)
	}
	if !strings.Contains(out.String(), "files=2") {
		t.Errorf("log = %q, want one warning naming 2 files", out.String())
	}
}
