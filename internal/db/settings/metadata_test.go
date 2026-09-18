package settings_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestMetadataSettings_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	got := cache.Snapshot().MetadataSettings()
	if want := (settings.MetadataSettings{BrainzMashUserAgent: "Lidarr/2.9.6.4552"}); got != want {
		t.Errorf("MetadataSettings = %+v, want %+v", got, want)
	}
}

func TestSetMetadataSettings_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.MetadataSettings{BrainzMashUserAgent: "Solarr/26.09 ( https://github.com/makearr/Solarr )"}
	if err := cache.SetMetadataSettings(t.Context(), cl, want); err != nil {
		t.Fatalf("SetMetadataSettings: %v", err)
	}
	if got := cache.Snapshot().MetadataSettings(); got != want {
		t.Errorf("MetadataSettings = %+v, want %+v", got, want)
	}
}

func TestSetMetadataSettings_RejectsAnUnsendableAgent(t *testing.T) {
	for name, agent := range map[string]string{
		"empty":        "",
		"newline":      "Lidarr/2.9\r\nX-Injected: 1",
		"non-ascii":    "Lidärr/2.9",
		"over the cap": strings.Repeat("a", 257),
	} {
		t.Run(name, func(t *testing.T) {
			cache, cl := newCache(t)
			if err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{BrainzMashUserAgent: agent}); err == nil {
				t.Fatalf("agent %q was accepted", agent)
			}
			if got := cache.Snapshot().MetadataSettings().BrainzMashUserAgent; got != settings.DefaultBrainzMashUserAgent {
				t.Errorf("stored agent = %q after a refused write, want the default", got)
			}
		})
	}
}
