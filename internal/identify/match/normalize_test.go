package match

import "testing"

// "The Tonhut" and "Tonhut, The" are section 10.2's own leading-article example
// and must normalize to the identical string, not merely a close one.
func TestNormalizeTitle_LeadingArticleBothForms(t *testing.T) {
	got := normalizeTitle("Tonhut, The")
	want := normalizeTitle("The Tonhut")
	if got != want {
		t.Fatalf("normalizeTitle(%q) = %q, normalizeTitle(%q) = %q, want equal",
			"Tonhut, The", got, "The Tonhut", want)
	}
	if want != "the tonhut" {
		t.Errorf("normalizeTitle(%q) = %q, want %q", "The Tonhut", want, "the tonhut")
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"case fold", "HEROES", "heroes"},
		{"accent fold", "Café Del Mar", "cafe del mar"},
		{"punctuation stripped", "Sci-Fi: The Album!", "sci fi the album"},
		{"separators collapsed", "too   many    spaces", "too many spaces"},
		{"trailing article A", "Bridge, A", "a bridge"},
		{"trailing article An", "Album, An", "an album"},
		{"no article, unaffected", "Songs About Nothing", "songs about nothing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeTitle(test.input); got != test.want {
				t.Errorf("normalizeTitle(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestJaroWinkler_IdenticalStringsScoreOne(t *testing.T) {
	if got := jaroWinkler("the tonhut", "the tonhut"); got != 1 {
		t.Errorf("jaroWinkler(identical) = %v, want 1", got)
	}
}

func TestJaroWinkler_CompletelyDifferentStringsScoreLow(t *testing.T) {
	if got := jaroWinkler("abcdef", "zyxwvu"); got > 0.3 {
		t.Errorf("jaroWinkler(disjoint) = %v, want a low score", got)
	}
}

func TestJaccardWords_SharedPrefixSubset(t *testing.T) {
	got := jaccardWords("greatest hits", "greatest hits volume two")
	if got <= 0 || got >= 1 {
		t.Errorf("jaccardWords(subset) = %v, want strictly between 0 and 1", got)
	}
}
