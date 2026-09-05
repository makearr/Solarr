package inspect

// Shape is ADR-48's SHAPE step, the structural half of it: whether a unit reads as
// one track, a whole album's worth of tracks, or an album split across a disc
// subfolder set GROUP already merged. This needs no media-type knowledge and no
// identity (unlike PACK, ADR-50) -- it is a pure function of what GROUP already
// found, so it lives here rather than layered on by a caller the way IDENTIFY is.
//
// PACK is deliberately not a Shape value here. ADR-50's pack is a relationship
// between an import source and several *different* works it produced -- a fact
// about several sibling units agreeing on structure and artist, not a property any
// one unit has on its own. That needs the pack/pack_item schema and cross-unit
// comparison (todo 4.11), not yet built; a caller with IDENTIFY results for every
// unit in a session is where PACK detection belongs once it exists.
type Shape string

const (
	// ShapeSingle is a unit of one media file -- a single, not an album.
	ShapeSingle Shape = "single"
	// ShapeSet is more than one media file with no disc-subfolder evidence -- an
	// album's worth of tracks in one place.
	ShapeSet Shape = "set"
	// ShapeMultiDisc is a unit GROUP merged from a disc-subfolder pattern
	// (CD1/CD2, Disc 1/Disc 2) -- still one work, one edition (ADR-50: "Multi-disc
	// is a pack kind, not a special case"), but worth keeping distinct from
	// ShapeSet since it is evidence of the original disc layout a later stage
	// (naming, or an eventual pack record) may still want.
	ShapeMultiDisc Shape = "multi_disc"
)

// ShapeOf classifies one unit structurally, from GROUP's own findings alone.
func ShapeOf(unit Unit) Shape {
	if len(unit.Media) <= 1 {
		return ShapeSingle
	}
	for _, signal := range unit.Evidence.Signals {
		if signal.Kind == SignalDiscSubfolder {
			return ShapeMultiDisc
		}
	}
	return ShapeSet
}
