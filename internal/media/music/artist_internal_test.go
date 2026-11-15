package music

import "testing"

// A release must credit the wanted artist by name; a name inside another name is not the artist (SRCH-3).
func TestSameArtist(t *testing.T) {
	for _, test := range []struct {
		released, wanted string
		want             bool
	}{
		{"Air Supply", "Air", false},
		{"Jefferson Airplane", "Air", false},
		{"Yesterday's Children", "Yes", false},
		{"Queens of the Stone Age", "Queen", false},
		{"Heartless Bastards", "Heart", false},
		{"Madonna", "Maroon 5", false},
		{"Santana", "Sinatra", false},
		{"Metallica", "Megadeth", false},
		{"Queen", "Queen", true},
		{"Queen + Adam Lambert", "Queen", true},
		{"Queen feat. Adam Lambert", "Queen", true},
		{"The Beatles", "Beatles", true},
		{"Beatles", "The Beatles", true},
		{"Sigur Ros", "Sigur Rós", true},
		{"Bjork", "Björk", true},
		{"ACDC", "AC/DC", true},
		{"Tom Petty and the Heartbreakers", "Tom Petty", true},
		{"Tom Petty", "Tom Petty and the Heartbreakers", true},
		{"Anyone", "Various Artists", true},
		{"", "Queen", true},
	} {
		if got := sameArtist(test.released, test.wanted); got != test.want {
			t.Errorf("sameArtist(%q, %q) = %v, want %v", test.released, test.wanted, got, test.want)
		}
	}
}
