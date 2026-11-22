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
	// SignalDiscStructure marks a Blu-ray or DVD folder (BDMV, VIDEO_TS) kept as one unit; Detail names the folders.
	SignalDiscStructure SignalKind = "disc_structure"
)

// Signal is one piece of evidence that shaped a unit's boundary.
type Signal struct {
	Kind   SignalKind
	Detail string
}

// Evidence is why a unit's files ended up grouped the way they did.
type Evidence struct {
	Signals []Signal
	// Ambiguous is true when grouping inferred a boundary from a weak signal (track continuity alone); ADR-48 says disagreement lowers confidence, so IDENTIFY/DECIDE should queue rather than auto-commit.
	Ambiguous bool
}

// Unit is one candidate group of media files ADR-48's grouping stage produced.
type Unit struct {
	// Directory is the unit's own root, relative to the inspected path -- the
	// directory its files were found under, or the parent of a disc-subfolder
	// merge (Album/CD1+CD2 reports Album, not either child).
	Directory string
	Media     []File
	// Other is every non-media file in Directory or its merged children, kept as evidence rather than discarded (ADR-48).
	Other    []File
	Evidence Evidence
}
