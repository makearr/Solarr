package inspect

// SignalKind names one of ADR-48's grouping signals.
type SignalKind string

// Every signal Group can produce, in the order section 11.9 weighs them.
const (
	SignalEmbeddedID        SignalKind = "embedded_release_id"
	SignalTagAgreement      SignalKind = "tag_agreement"
	SignalDiscSubfolder     SignalKind = "disc_subfolder"
	SignalDirectoryBoundary SignalKind = "directory_boundary"
	SignalTrackContinuity   SignalKind = "track_continuity"
)

// Signal is one piece of evidence that shaped a unit's boundary.
type Signal struct {
	Kind   SignalKind
	Detail string
}

// Evidence is why a unit's files ended up grouped the way they did.
type Evidence struct {
	Signals []Signal
	// Ambiguous is true when grouping had to infer a boundary from a weaker
	// signal (track continuity alone) rather than a decisive one (an embedded
	// id, or a directory with nothing to contradict it) -- ADR-48:
	// "disagreement between signals lowers confidence; it does not pick a
	// winner silently." A later IDENTIFY/DECIDE stage is expected to treat
	// this as a reason to queue rather than auto-commit.
	Ambiguous bool
}

// Unit is one candidate group of media files ADR-48's grouping stage produced.
type Unit struct {
	// Directory is the unit's own root, relative to the inspected path -- the
	// directory its files were found under, or the parent of a disc-subfolder
	// merge (Album/CD1+CD2 reports Album, not either child).
	Directory string
	Media     []File
	// Other is every non-media file Run found in Directory or one of its
	// merged children -- artwork, sidecars and the rest, kept as evidence for
	// a later stage rather than discarded (ADR-48: "unknown does not mean
	// discard").
	Other    []File
	Evidence Evidence
}
