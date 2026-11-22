package inspect

// Shape is ADR-48's structural SHAPE step: one track, a whole album's tracks, or an album GROUP merged from disc subfolders; it is a pure function of what GROUP found.
// PACK is not a Shape value: ADR-50's pack relates several different works from one source, which needs cross-unit comparison (todo 4.11), not a property of one unit.
type Shape string

// Values match migration 0018's comment on import_unit.shape verbatim; library.UnitSingle/UnitSet/UnitMultiDisc re-export them as plain strings.
const (
	// ShapeSingle is a unit of one media file -- a single, not an album.
	ShapeSingle Shape = "SINGLE"
	// ShapeSet is more than one media file with no disc-subfolder evidence -- an
	// album's worth of tracks in one place.
	ShapeSet Shape = "SET"
	// ShapeMultiDisc is a unit GROUP merged from a disc-subfolder pattern (CD1/CD2): one work and edition (ADR-50), kept distinct from ShapeSet as evidence of the disc layout.
	ShapeMultiDisc Shape = "MULTI_DISC"
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
