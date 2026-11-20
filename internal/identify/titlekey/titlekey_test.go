package titlekey

import "testing"

func TestFold(t *testing.T) {
	tests := []struct {
		name  string
		title string
		o     Options
		want  string
	}{
		{"compact folds accents", "Les Misérables", Compact, "lesmiserables"},
		{"compact spells ampersand", "Law & Order", Compact, "lawandorder"},
		{"compact drops apostrophes", "Straight A’s", Compact, "straightas"},
		{"compact keeps the article", "The Wire", Compact, "thewire"},
		{"words separates", "Spider-Man: Far From Home", Words, "spider man far from home"},
		{"words spells ampersand", "Rock&Roll", Words, "rock and roll"},
		{"words unfolds a ligature", "Straße Æon", Words, "strasse aeon"},
		{"words decomposed accent", "Beyoncé", Words, "beyonce"},
		{"article dropped", "The Example", WordsNoArticle, "example"},
		{"a lone article stays", "The", WordsNoArticle, "the"},
		{"empty", " - ", Words, ""},
	}
	for _, test := range tests {
		if got := Fold(test.title, test.o); got != test.want {
			t.Errorf("%s: Fold(%q) = %q, want %q", test.name, test.title, got, test.want)
		}
	}
}
