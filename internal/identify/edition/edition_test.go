package edition_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/identify/edition"
)

func TestCodeAndFromName(t *testing.T) {
	for in, want := range map[string]string{"en-US": "en", "ENG": "en", "German": "de", "": "", "tlh": ""} {
		if got := edition.Code(in); got != want {
			t.Errorf("Code(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"Title [German] 2004":  "de",
		"Title (fr).epub":      "fr",
		"Title.French.epub":    "fr",
		"It Follows":           "",
		"Title 03 - It":        "",
		"A Tale (Italian ed.)": "it",
	} {
		if got := edition.FromName(in); got != want {
			t.Errorf("FromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNarrowPrefersTheFilesLanguageThenTheLibrarys(t *testing.T) {
	type one struct{ id, language string }
	editions := []one{{"a", "fr"}, {"b", "en"}, {"c", ""}}
	language := func(e one) string { return e.language }
	cases := map[string]struct {
		file      string
		preferred []string
		want      []one
	}{
		"the file":              {"fr", []string{"en"}, []one{{"a", "fr"}}},
		"the library":           {"", []string{"de", "en"}, []one{{"b", "en"}}},
		"nothing fits":          {"", []string{"de"}, editions},
		"a file's tag it lacks": {"de", []string{"en"}, []one{{"b", "en"}}},
	}
	for name, test := range cases {
		if got := edition.Narrow(editions, language, test.file, test.preferred); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: %v, want %v", name, got, test.want)
		}
	}
}
