package archtest

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mediaRegistration is the one file that may import a media module: it registers them.
const mediaRegistration = "cmd/solarr/app/registered.go"

// mediaModulePrefix is where media types live; internal/media itself is the registry everyone may ask.
const mediaModulePrefix = "internal/media/"

// directWiredMediaFiles still import a media module directly. The list only shrinks: each entry
// goes when its file asks the registry instead, and nothing is ever added.
var directWiredMediaFiles = []string{
	"cmd/solarr/adapters/arrsync.go",
	"cmd/solarr/adapters/arrsync_sonarr.go",
	"cmd/solarr/adapters/artistactions.go",
	"cmd/solarr/adapters/artwork.go",
	"cmd/solarr/adapters/candidatesearch.go",
	"cmd/solarr/adapters/discover.go",
	"cmd/solarr/adapters/duplicates.go",
	"cmd/solarr/adapters/importsessions.go",
	"cmd/solarr/adapters/people.go",
	"cmd/solarr/adapters/seasonsearch.go",
	"cmd/solarr/adapters/series_cutoff.go",
	"cmd/solarr/adapters/series.go",
	"cmd/solarr/adapters/seriespeople.go",
	"cmd/solarr/adapters/unidentified.go",
	"cmd/solarr/adapters/unidentified_tv.go",
	"cmd/solarr/adapters/worksmonitor.go",
	"cmd/solarr/app/artwork_choice.go",
	"cmd/solarr/app/fingerprint_retry.go",
	"cmd/solarr/app/job_artwork.go",
	"cmd/solarr/app/job_bridge.go",
	"cmd/solarr/app/job_cutoff.go",
	"cmd/solarr/app/job_discography.go",
	"cmd/solarr/app/job_enrich.go",
	"cmd/solarr/app/job_import.go",
	"cmd/solarr/app/job_search.go",
	"cmd/solarr/app/job_subscription.go",
	"cmd/solarr/app/job_tagwriteback.go",
	"cmd/solarr/app/job_tvrefresh.go",
	"cmd/solarr/app/providers.go",
	"cmd/solarr/app/serve.go",
	"internal/importing/importjob/job.go",
	"internal/importing/importjob/manualcommit.go",
}

func TestMediaModules_AreImportedOnlyByTheRegistration(t *testing.T) {
	root := repoRoot(t)
	module := modulePath(t, root)

	var importers []string
	err := filepath.WalkDir(root, func(fullPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, fullPath)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && (strings.HasPrefix(entry.Name(), ".") || slices.Contains(skippedDirs, entry.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") || strings.HasPrefix(relative, mediaModulePrefix) {
			return nil
		}
		for _, imported := range internalImportsOf(t, fullPath, module) {
			if strings.HasPrefix(imported, mediaModulePrefix) {
				importers = append(importers, relative)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	for _, importer := range importers {
		if importer != mediaRegistration && !slices.Contains(directWiredMediaFiles, importer) {
			t.Errorf("%s imports a media module; ask the media registry instead (internal/media)", importer)
		}
	}
	for _, listed := range directWiredMediaFiles {
		if !slices.Contains(importers, listed) {
			t.Errorf("%s no longer imports a media module; remove it from directWiredMediaFiles", listed)
		}
	}
}
