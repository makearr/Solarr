package metaresolve_test

import (
	"maps"
	"testing"

	"github.com/makearr/Solarr/internal/metadata/metaresolve"
)

func TestResolve(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: "Newer synopsis", FetchedAt: 9},
		{Field: "overview", Provider: metaresolve.ManualProvider, Value: "My synopsis", FetchedAt: 2},
		{Field: "status", Provider: "skyhook", Value: "continuing", FetchedAt: 1},
		{Field: "status", Provider: "tmdb", Value: "ended", FetchedAt: 5},
		{Field: "network", Provider: "tmdb", Value: "", FetchedAt: 8},
		{Field: "network", Provider: "skyhook", Value: "Northern Channel", FetchedAt: 3},
		{Field: "country", Provider: "tvmaze", Value: "gb", FetchedAt: 4},
		{Field: "country", Provider: "skyhook", Value: "ie", FetchedAt: 4},
		{Field: "logo", Provider: "skyhook", Value: "", FetchedAt: 1},
	}
	want := map[string]metaresolve.Value{
		"overview": values[1], // a person's own beats a newer provider value
		"status":   values[3], // the newest provider value
		"network":  values[5], // an empty value never wins
		"country":  values[7], // a tie goes to the provider's name
	}
	if got := metaresolve.Resolve(values); !maps.Equal(got, want) {
		t.Errorf("Resolve = %v, want %v", got, want)
	}
}

func TestOrderSQL(t *testing.T) {
	want := "(f.provider = '__manual__') DESC, f.fetched_at DESC, f.provider"
	if got := metaresolve.OrderSQL("f"); got != want {
		t.Errorf("OrderSQL = %q, want %q", got, want)
	}
}
