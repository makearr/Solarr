package app

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Every client in the process must share one budget (ADR-13): separate ones let concurrent jobs exceed a
// provider's pace, which is how MusicBrainz came to answer 503.
func TestOutbound_IsOnePerProcess(t *testing.T) {
	p := &process{}

	first, second := p.outbound(), p.outbound()
	if first.limiter == nil || first.breaker == nil {
		t.Fatalf("budget = %+v, want a limiter and a breaker", first)
	}
	if first.limiter != second.limiter || first.breaker != second.breaker {
		t.Error("two calls built two budgets, want the same limiter and breaker every time")
	}
}

// BrainzMash answers ws/2 at ten times direct's rate, so it is tried first by default.
func TestMusicBrainzSources_DefaultOrderPutsBrainzMashFirst(t *testing.T) {
	p := &process{}

	got := sourceNames(p.musicBrainzSources(config.Default().Metadata.MusicBrainz))
	want := []string{musicbrainz.BrainzMashName, musicbrainz.ProxyName, musicbrainz.DirectName}
	if !slices.Equal(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
}

// Direct MusicBrainz is what every other source falls back to, so disabling the rest keeps it.
func TestMusicBrainzSources_DirectRemainsWhenEverythingElseIsOff(t *testing.T) {
	p := &process{}
	sources := config.Default().Metadata.MusicBrainz
	sources.BrainzMashEnabled, sources.ProxyEnabled = false, false

	got := sourceNames(p.musicBrainzSources(sources))
	if want := []string{musicbrainz.DirectName}; !slices.Equal(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
}

func sourceNames(sources []musicbrainz.Source) []string {
	names := make([]string, len(sources))
	for i, source := range sources {
		names[i] = source.Name()
	}
	return names
}
