package music

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestHealthChecks_WarnsOnlyWhenALibraryExistsAndTheKeyIsMissing(t *testing.T) {
	missing := Module{AcoustIDConfigured: func() bool { return false }}
	want := []media.HealthCheck{{
		ID: "music_no_acoustid_key", Severity: media.HealthWarning,
		Message: "Files without tags cannot be recognised: add your free AcoustID key under Metadata.",
		Link:    "/settings/metadata",
	}}
	if got := missing.HealthChecks(1); !reflect.DeepEqual(got, want) {
		t.Fatalf("checks = %+v, want %+v", got, want)
	}
	if got := missing.HealthChecks(0); got != nil {
		t.Fatalf("no library: checks = %+v, want none", got)
	}
	if got := (Module{AcoustIDConfigured: func() bool { return true }}).HealthChecks(1); got != nil {
		t.Fatalf("key set: checks = %+v, want none", got)
	}
}
