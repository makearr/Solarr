package knownfor

import "testing"

// A credit's title and a library title meet on letters and digits, whatever the accents, ampersand or apostrophes.
func TestTitleKey_MeetsSpellingsOfOneTitle(t *testing.T) {
	for _, pair := range [][2]string{
		{"JEHES", "jehes"}, {"Fubar!", "fubar"}, {"Café Noir", "Cafe Noir"}, {"Salt & Pepper", "Salt and Pepper"}, {"Shep's Row", "Sheps Row"},
	} {
		if titleKey(pair[0]) != titleKey(pair[1]) {
			t.Errorf("titleKey(%q) = %q, titleKey(%q) = %q; want one key", pair[0], titleKey(pair[0]), pair[1], titleKey(pair[1]))
		}
	}
	if titleKey("Fubar") == titleKey("Fubar Two") {
		t.Error("two different titles share one key")
	}
}
