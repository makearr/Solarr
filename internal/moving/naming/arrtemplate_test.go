package naming_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/moving/naming"
)

func TestTranslateArrTemplate_CarriesWhatSolarrKnowsAndNamesWhatItDrops(t *testing.T) {
	known := map[string]bool{"Series Title": true, "season": true, "episode": true, "Episode Title": true, "Quality Full": true, "Release Group": true, "Release Year": true}
	for _, check := range []struct {
		name, spec string
		want       naming.ArrTranslation
	}{
		{"a Sonarr default", "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}",
			naming.ArrTranslation{Template: "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}"}},
		{"a prefix that shows only with a value becomes a conditional", "{Series Title} {Quality Full}{-Release Group}",
			naming.ArrTranslation{Template: "{Series Title} {Quality Full}[-{Release Group}]"}},
		{"parentheses around a token stay inside the conditional", "{Series Title}{ (Release Year)}",
			naming.ArrTranslation{Template: "{Series Title}[ ({Release Year})]"}},
		{"square brackets cannot be spelled in a conditional", "{Series Title} {[Quality Full]}",
			naming.ArrTranslation{Template: "{Series Title} {Quality Full}", Notes: []string{"{[Quality Full]}: the square brackets around it are not carried over"}}},
		{"literal brackets are escaped", "[{Series Title}]", naming.ArrTranslation{Template: "[[{Series Title}]]"}},
		{"a token Solarr lacks is dropped, named and tidied away", "{Series Title} ({Edition Tags}) - {Custom Formats}",
			naming.ArrTranslation{Template: "{Series Title}", Dropped: []string{"{Edition Tags}", "{Custom Formats}"}}},
		{"a dropped token between folders leaves no space at the slash", "{Series Title}/{Edition Tags} {Episode Title}",
			naming.ArrTranslation{Template: "{Series Title}/{Episode Title}", Dropped: []string{"{Edition Tags}"}}},
		{"a length on a text token is a truncation", "{Series Title:30}", naming.ArrTranslation{Template: "{Series Title:.30}"}},
		{"a format Solarr cannot spell is left off", "{Series Title:30-}", naming.ArrTranslation{Template: "{Series Title}", Notes: []string{"{Series Title:30-}: the format :30- is not carried over"}}},
		{"escaped braces stay", "{{{Series Title}}}", naming.ArrTranslation{Template: "{{{Series Title}}}"}},
	} {
		if got := naming.TranslateArrTemplate(check.spec, known); !reflect.DeepEqual(got, check.want) {
			t.Errorf("%s: got %+v, want %+v", check.name, got, check.want)
		}
		if got := naming.TranslateArrTemplate(check.spec, known); got.Template != "" {
			segments, err := naming.Compile(got.Template)
			if err != nil || len(naming.UnknownTokens(segments, known)) > 0 {
				t.Errorf("%s: %q does not compile with the scheme's tokens: %v", check.name, got.Template, err)
			}
		}
	}
}

// Title and CleanTitle are different tokens in every app: a template keeps the one it names.
func TestTranslateArrTemplate_NeverTurnsATitleIntoACleanTitleOrBack(t *testing.T) {
	known := map[string]bool{"Release Year": true}
	pairs := [][2]string{
		{"Movie Title", "Movie CleanTitle"}, {"Series Title", "Series CleanTitle"}, {"Episode Title", "Episode CleanTitle"},
		{"Book Title", "Book CleanTitle"}, {"Author Name", "Author CleanName"}, {"Scene Title", "Scene CleanTitle"},
		{"Studio Name", "Studio CleanName"}, {"Artist Name", "Artist CleanName"}, {"Album Title", "Album CleanTitle"},
		{"Track Title", "Track CleanTitle"}, {"Movie TitleThe", "Movie CleanTitleThe"}, {"Series TitleThe", "Series CleanTitleThe"},
	}
	for _, pair := range pairs {
		known[pair[0]], known[pair[1]] = true, true
	}
	for _, pair := range pairs {
		for _, name := range pair {
			if got := naming.TranslateArrTemplate("{"+name+"} ({Release Year})", known).Template; got != "{"+name+"} ({Release Year})" {
				t.Errorf("%s became %q", name, got)
			}
		}
	}
}
