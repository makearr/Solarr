package parse

import "testing"

func TestSegmentConfidence(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		wantLow bool // true when the segment must score below inspect's own 0.2 container threshold
	}{
		{"artist name", "Katupoken", false},
		{"album with year", "KI Pecezinu (1997)", false},
		{"multi-word artist", "The Sizalige Mapokupi", false},
		{"bare year", "1997", true},
		{"bracketed bare year", "(1997)", true},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"hashed junk", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4", true},
		{"disc container", "CD1", true},
		{"format container", "FLAC", true},
		{"edition word alone", "Deluxe Edition", true},
		{"source word alone", "WEB", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SegmentConfidence(tt.segment)
			low := got < 0.2
			if low != tt.wantLow {
				t.Errorf("SegmentConfidence(%q) = %v, low = %v, want low = %v", tt.segment, got, low, tt.wantLow)
			}
		})
	}
}
