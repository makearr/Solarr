package book

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/media"
)

func TestEditionsOf_KeepsTheEditionTheFileSaysThenTheLibrarysLanguage(t *testing.T) {
	candidates := []Candidate{
		{Title: "The Lantern Keeper", Author: "Mara Quell", Traits: media.Traits{OriginalLanguage: "fr"}},
		{Title: "The Lantern Keeper", Author: "Mara Quell", Traits: media.Traits{OriginalLanguage: "en"}},
		{Title: "Another Book", Author: "Mara Quell", Traits: media.Traits{OriginalLanguage: "fr"}},
	}
	resolver := Resolver{Languages: func(int64) []string { return []string{"en"} }}
	got := resolver.editionsOf(candidates, 1, parsebook.Info{})
	if len(got) != 2 || got[0].Traits.OriginalLanguage != "en" || got[1].Title != "Another Book" {
		t.Errorf("library preference: %+v", got)
	}
	got = resolver.editionsOf(candidates, 1, parsebook.Info{Language: "fr"})
	if len(got) != 2 || got[0].Traits.OriginalLanguage != "fr" {
		t.Errorf("file language: %+v", got)
	}
}

func TestParse_ReadsALanguageTagFromTheName(t *testing.T) {
	info, ok := parsebook.Parse("Mara Quell - The Lantern Keeper [German].epub")
	if !ok || info.Language != "de" {
		t.Errorf("language = %q (ok %v), want de", info.Language, ok)
	}
}
