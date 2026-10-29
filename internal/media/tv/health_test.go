package tv_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/media/tv"
)

func TestHealthChecksAsksForTheKeyOnlyWhereATVLibraryLacksIt(t *testing.T) {
	missing := tv.Module{TMDBConfigured: func() bool { return false }}
	if checks := missing.HealthChecks(1); len(checks) != 1 || checks[0].ID != "tv_no_tmdb_key" || checks[0].Link != "/settings/metadata" {
		t.Errorf("checks = %+v, want one warning linking to Metadata", checks)
	}
	if checks := missing.HealthChecks(0); len(checks) != 0 {
		t.Errorf("no TV library: %+v, want none", checks)
	}
	if checks := (tv.Module{TMDBConfigured: func() bool { return true }}).HealthChecks(2); len(checks) != 0 {
		t.Errorf("key stored: %+v, want none", checks)
	}
	if checks := (tv.Module{}).HealthChecks(1); len(checks) != 0 {
		t.Errorf("not wired: %+v, want none", checks)
	}
}

func TestHealthChecksNamesEachPausedSource(t *testing.T) {
	module := tv.Module{SourcesPaused: func() []string { return []string{"Skyhook", "TVmaze"} }}
	checks := module.HealthChecks(1)
	if len(checks) != 2 || checks[0].ID != "tv_source_down_skyhook" || checks[1].ID != "tv_source_down_tvmaze" {
		t.Errorf("checks = %+v, want one warning per paused source", checks)
	}
	if checks := module.HealthChecks(0); len(checks) != 0 {
		t.Errorf("no TV library: %+v, want none", checks)
	}
}
