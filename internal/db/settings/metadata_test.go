package settings_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
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

// The enrichment switches (2026-09-24): nil leaves a value alone, an empty cover list turns every
// cover source off, and an unknown source is refused.
func TestSetMetadataSettings_EnrichmentSwitches(t *testing.T) {
	cache, cl := newCache(t)
	stored := cache.Snapshot().MetadataSettings()
	if !reflect.DeepEqual(stored.CoverSources, settings.DefaultCoverSources) || !*stored.Wikidata || !*stored.AcoustID {
		t.Fatalf("defaults = %+v", stored)
	}

	off := false
	err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent,
		CoverSources:        []string{settings.CoverSourceEmbedded}, Wikidata: &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored = cache.Snapshot().MetadataSettings()
	if !reflect.DeepEqual(stored.CoverSources, []string{"embedded"}) || *stored.Wikidata || !*stored.AcoustID {
		t.Errorf("after the write = %+v, want embedded only, Wikidata off, AcoustID untouched", stored)
	}

	err = cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent, CoverSources: []string{"fanart"},
	})
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Errorf("an unknown cover source: err = %v, want a problem", err)
	}
}

// The artist backfill's per-run budget: 200 by default, any write from 1 to 10,000 kept, nil untouched.
func TestSetMetadataSettings_PeopleBackfillPerRun(t *testing.T) {
	cache, cl := newCache(t)
	if got := *cache.Snapshot().MetadataSettings().PeopleBackfillPerRun; got != settings.DefaultPeopleBackfillPerRun {
		t.Errorf("default = %d, want %d", got, settings.DefaultPeopleBackfillPerRun)
	}
	perRun := int64(500)
	if err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent, PeopleBackfillPerRun: &perRun,
	}); err != nil {
		t.Fatalf("SetMetadataSettings: %v", err)
	}
	if err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
		BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent,
	}); err != nil {
		t.Fatalf("SetMetadataSettings without it: %v", err)
	}
	if got := *cache.Snapshot().MetadataSettings().PeopleBackfillPerRun; got != 500 {
		t.Errorf("stored = %d, want 500 kept across a write that left it out", got)
	}
	for _, refused := range []int64{0, -1, 10001} {
		err := cache.SetMetadataSettings(t.Context(), cl, settings.MetadataSettings{
			BrainzMashUserAgent: settings.DefaultBrainzMashUserAgent, PeopleBackfillPerRun: &refused,
		})
		var problems config.Problems
		if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Path != settings.KeyMetadataPeopleBackfillPerRun {
			t.Errorf("%d: err = %v, want one problem on %s", refused, err, settings.KeyMetadataPeopleBackfillPerRun)
		}
	}
}
