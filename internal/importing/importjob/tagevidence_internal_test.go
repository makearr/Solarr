package importjob

import (
	"context"
	"testing"

	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
)

type plainImporter struct{ importbatch.Importer }

type taggingImporter struct{ importbatch.Importer }

func (taggingImporter) InspectTags(context.Context, string, []string) (inspect.TagGrouper, error) {
	return nil, nil
}

func TestWalkOptions_TagsForComesFromTheImporterThatReadsTags(t *testing.T) {
	job := &Job{
		importers:   map[string]importbatch.Importer{"music": taggingImporter{}, "tv": plainImporter{}},
		mediaTypeOf: func(id int64) string { return map[int64]string{1: "music", 2: "tv"}[id] },
		unassigned:  "music",
	}
	one, two := int64(1), int64(2)
	for name, tc := range map[string]struct {
		payload Payload
		want    bool
	}{
		"a music library":           {Payload{CategoryID: &one}, true},
		"a tv library":              {Payload{CategoryID: &two}, false},
		"no library, unassigned":    {Payload{}, true},
		"best match with no choice": {Payload{BestMatch: true}, true},
	} {
		if got := job.walkOptions(tc.payload).TagsFor != nil; got != tc.want {
			t.Errorf("%s: TagsFor set = %v, want %v", name, got, tc.want)
		}
	}
}

// A Best match folder is grouped by tags even when the unassigned library's type does not read them.
func TestWalkOptions_BestMatchFindsAnyImporterThatReadsTags(t *testing.T) {
	job := &Job{
		importers:   map[string]importbatch.Importer{"music": taggingImporter{}, "tv": plainImporter{}},
		mediaTypeOf: func(int64) string { return "" },
		unassigned:  "tv",
	}
	for name, tc := range map[string]struct {
		payload Payload
		want    bool
	}{
		"best match, unassigned is tv": {Payload{BestMatch: true}, true},
		"no library, unassigned is tv": {Payload{}, false},
	} {
		if got := job.walkOptions(tc.payload).TagsFor != nil; got != tc.want {
			t.Errorf("%s: TagsFor set = %v, want %v", name, got, tc.want)
		}
	}
}
