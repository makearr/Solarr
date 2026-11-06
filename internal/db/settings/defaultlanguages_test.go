package settings_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestDefaultLanguages_AreKeptInOrderAndValidated(t *testing.T) {
	cache, _ := newCache(t)
	if got := cache.Snapshot().DefaultLanguages(); len(got) != 0 {
		t.Fatalf("a fresh instance has defaults %v, want none until the wizard asks", got)
	}
	if err := cache.SetDefaultLanguages(t.Context(), []string{"DE", " en ", "de"}); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().DefaultLanguages(); !reflect.DeepEqual(got, []string{"de", "en"}) {
		t.Errorf("defaults = %v, want [de en]: lower case, in order, listed once", got)
	}
	if err := cache.SetDefaultLanguages(t.Context(), []string{"english"}); err == nil {
		t.Error("a language name instead of a code was accepted")
	}
}

func TestLanguageProfileFor_PutsJapaneseFirstForAnimeAndKeepsTheDefaultsAsSubtitles(t *testing.T) {
	plain := settings.LanguageProfileFor([]string{"en", "de"}, false)
	if plain.Name != "Prefer en, de" || !reflect.DeepEqual(plain.AudioPreferred, []string{"en", "de"}) || len(plain.SubtitlePreferred) != 0 {
		t.Errorf("plain = %+v", plain)
	}
	anime := settings.LanguageProfileFor([]string{"en", "ja", "de"}, true)
	if anime.Name != "Japanese, then en, ja, de" || !reflect.DeepEqual(anime.AudioPreferred, []string{"ja", "en", "de"}) || !reflect.DeepEqual(anime.SubtitlePreferred, []string{"en", "ja", "de"}) {
		t.Errorf("anime = %+v", anime)
	}
	if empty := settings.LanguageProfileFor(nil, false); !reflect.DeepEqual(empty.AudioPreferred, []string{"en"}) {
		t.Errorf("no defaults = %+v, want English", empty)
	}
}
