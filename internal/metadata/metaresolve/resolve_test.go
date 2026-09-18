package metaresolve_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/metadata/metaresolve"
)

func resolveField(t *testing.T, values []metaresolve.Value, priority []string, field string) (metaresolve.Resolved, bool) {
	t.Helper()
	resolved := metaresolve.Resolve(values, priority)
	value, ok := resolved[field]
	return value, ok
}

// Invariant 13, in its strongest form: a manual override beats even the
// first-priority provider.
func TestResolve_ManualOverrideAlwaysWins(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: "Provider synopsis", FetchedAt: 1},
		{Field: "overview", Provider: metaresolve.ManualProvider, Value: "My synopsis", FetchedAt: 2},
	}

	got, ok := resolveField(t, values, []string{"tmdb", "imdb"}, "overview")
	if !ok {
		t.Fatal("overview did not resolve")
	}
	if got.Value != "My synopsis" || got.Provider != metaresolve.ManualProvider {
		t.Errorf("got %+v, want the manual override", got)
	}
}

// "Never overwritten by a refresh" includes a user clearing the field on purpose --
// that is still an override, not the absence of one.
func TestResolve_ManualOverrideWinsEvenWhenEmpty(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: "Provider synopsis"},
		{Field: "overview", Provider: metaresolve.ManualProvider, Value: ""},
	}

	got, ok := resolveField(t, values, []string{"tmdb"}, "overview")
	if !ok {
		t.Fatal("overview did not resolve")
	}
	if got.Provider != metaresolve.ManualProvider || got.Value != "" {
		t.Errorf("got %+v, want the empty manual override", got)
	}
}

// The exact ADR-8 example: a provider ranked first but missing the field must not
// blank it -- the next provider that actually has a value wins.
func TestResolve_FallsThroughAProviderMissingTheField(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "imdb", Value: "IMDb synopsis"},
	}

	got, ok := resolveField(t, values, []string{"tmdb", "imdb"}, "overview")
	if !ok {
		t.Fatal("overview did not resolve")
	}
	if got.Provider != "imdb" {
		t.Errorf("provider = %q, want imdb: tmdb has no row for this field at all", got.Provider)
	}
}

// A provider that has a row but an empty value is exactly as absent as no row at
// all -- an empty string must not win just for being first in priority.
func TestResolve_SkipsAProviderWithAnEmptyValue(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: ""},
		{Field: "overview", Provider: "imdb", Value: "IMDb synopsis"},
	}

	got, ok := resolveField(t, values, []string{"tmdb", "imdb"}, "overview")
	if !ok {
		t.Fatal("overview did not resolve")
	}
	if got.Provider != "imdb" {
		t.Errorf("provider = %q, want imdb: tmdb's value is empty", got.Provider)
	}
}

// Priority is a scope, not a ranking of everything that happens to have a value --
// a provider left out of it must never win.
func TestResolve_IgnoresAProviderNotInPriority(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "some-scraper", Value: "Unranked value"},
	}

	_, ok := resolveField(t, values, []string{"tmdb", "imdb"}, "overview")
	if ok {
		t.Error("overview resolved, want it left out: no priority provider has a value")
	}
}

func TestResolve_ResolvesEachFieldIndependently(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: "Synopsis"},
		{Field: "tagline", Provider: "imdb", Value: "Tagline"},
	}

	resolved := metaresolve.Resolve(values, []string{"tmdb", "imdb"})
	if len(resolved) != 2 {
		t.Fatalf("got %d resolved fields, want 2: %+v", len(resolved), resolved)
	}
	if resolved["overview"].Provider != "tmdb" || resolved["tagline"].Provider != "imdb" {
		t.Errorf("resolved = %+v, want overview from tmdb and tagline from imdb", resolved)
	}
}

func TestResolve_OmitsAFieldWithNothingEligible(t *testing.T) {
	values := []metaresolve.Value{
		{Field: "overview", Provider: "tmdb", Value: ""},
	}

	resolved := metaresolve.Resolve(values, []string{"tmdb"})
	if _, ok := resolved["overview"]; ok {
		t.Errorf("overview resolved to %+v, want it omitted entirely", resolved["overview"])
	}
}
