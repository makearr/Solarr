package settings_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestMetadataSettings_DefaultsWhenNothingIsStored(t *testing.T) {
	cache, _ := newCache(t)
	got := cache.Snapshot().MetadataSettings()
	if got.BrainzMashUserAgent != "Lidarr/2.9.6.4552" {
		t.Errorf("BrainzMashUserAgent = %q", got.BrainzMashUserAgent)
	}
	if !slices.Equal(got.SourceOrder, settings.DefaultMetadataSourceOrder) {
		t.Errorf("SourceOrder = %v, want the default %v", got.SourceOrder, settings.DefaultMetadataSourceOrder)
	}
}

func TestSetMetadataSettings_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.MetadataSettings{
		BrainzMashUserAgent: "Solarr/26.09 ( https://github.com/makearr/Solarr )",
		SourceOrder:         settings.DefaultMetadataSourceOrder,
	}
	if err := cache.SetMetadataSettings(t.Context(), cl, want); err != nil {
		t.Fatalf("SetMetadataSettings: %v", err)
	}
	got := cache.Snapshot().MetadataSettings()
	if got.BrainzMashUserAgent != want.BrainzMashUserAgent || !slices.Equal(got.SourceOrder, want.SourceOrder) {
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

// Direct MusicBrainz is the one source always reachable; the others are
// proxies of it, so it can be reordered but never removed.
func TestSetMetadataSettings_RefusesToRemoveDirectMusicBrainz(t *testing.T) {
	cache, cl := newCache(t)
	err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent,
		SourceOrder:         []string{settings.SourceBrainzMash},
	})
	if err == nil {
		t.Fatal("removing direct MusicBrainz was accepted, want a validation error")
	}
}

func TestSetMetadataSettings_RefusesAnUnknownOrDuplicateSource(t *testing.T) {
	cache, cl := newCache(t)
	for name, order := range map[string][]string{
		"unknown":   {settings.SourceMusicBrainz, "discogs"},
		"duplicate": {settings.SourceMusicBrainz, settings.SourceBrainzMash, settings.SourceBrainzMash},
	} {
		err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
			BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent, SourceOrder: order,
		})
		if err == nil {
			t.Errorf("%s source order was accepted, want a validation error", name)
		}
	}
}

// The order is what comes back, not a canonical one: reordering is the point.
func TestSetMetadataSettings_KeepsTheChosenOrder(t *testing.T) {
	cache, cl := newCache(t)
	order := []string{settings.SourceMusicBrainz, settings.SourceBrainzMash}
	if err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent, SourceOrder: order,
	}); err != nil {
		t.Fatalf("SetMetadataSettings: %v", err)
	}
	if got := cache.Snapshot().MetadataSettings().SourceOrder; !slices.Equal(got, order) {
		t.Errorf("SourceOrder = %v, want %v", got, order)
	}
}
