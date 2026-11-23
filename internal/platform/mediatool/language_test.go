package mediatool_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestLanguageCode3(t *testing.T) {
	for in, want := range map[string]string{"en": "eng", "DE": "deu", "ja": "jpn", "eng": "eng", "xx": "xx", "": ""} {
		if got := mediatool.LanguageCode3(in); got != want {
			t.Errorf("LanguageCode3(%q) = %q, want %q", in, got, want)
		}
	}
}
