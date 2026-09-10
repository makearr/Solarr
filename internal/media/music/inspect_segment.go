package music

import (
	"github.com/makearr/MediaNexus/internal/identify/parse"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
)

// InspectSegmentPlausible adapts parse.SegmentConfidence to inspect's own
// SegmentPlausibility signature -- music is the caller with real vocabulary
// that internal/importing/inspect.Options.SegmentPlausible asks for (ADR-23), so a
// directory named after a real edition/source/format word ("WEB", "Deluxe
// Edition", "CD1") is recognised as a container rather than an artist or
// album, without internal/importing/inspect itself carrying any music vocabulary.
var InspectSegmentPlausible inspect.SegmentPlausibility = func(segment string) (float64, bool) {
	return parse.SegmentConfidence(segment), true
}
