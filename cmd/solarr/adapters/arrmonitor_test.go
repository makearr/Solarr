package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestMonitorChoiceUsesTheTypesOwnWordsForMonitoredAndNot(t *testing.T) {
	for _, test := range []struct {
		name      string
		options   []string
		monitored bool
		want      string
		known     bool
	}{
		{"tv monitors all", []string{"future", "all", "none"}, true, "all", true},
		{"movie", []string{"movie_only", "movie_and_collection", "none"}, true, "movie_only", true},
		{"scene", []string{"scene_only", "none"}, true, "scene_only", true},
		{"unmonitored is none", []string{"scene_only", "none"}, false, "none", true},
		{"no options", nil, true, "", false},
		{"no none to stop with", []string{"all"}, false, "", false},
	} {
		var descriptor media.Descriptor
		for _, key := range test.options {
			descriptor.MonitorOptions = append(descriptor.MonitorOptions, media.Choice{Key: key})
		}
		got, known := monitorChoice(descriptor, test.monitored)
		if got != test.want || known != test.known {
			t.Errorf("%s: got %q, %v; want %q, %v", test.name, got, known, test.want, test.known)
		}
	}
}
