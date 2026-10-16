package adapters

import "testing"

func TestRadarrAvailabilityReadsRadarrsWordsAndLeavesUnknownOnesToTheLibrary(t *testing.T) {
	for value, want := range map[string]string{
		"announced": "announced", "tba": "announced", "inCinemas": "in_cinemas", "released": "released", "preDB": "released", "": "", "somethingNew": "",
	} {
		if got := radarrAvailability(value); got != want {
			t.Errorf("radarrAvailability(%q) = %q, want %q", value, got, want)
		}
	}
}
