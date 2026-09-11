package model

// Attributes is what a release name *claims* about the file itself, as opposed to
// Identity's claim about what work it is (ADR-39: "release name = claims, ffprobe =
// facts. Facts win. Both stored" -- this is the claims half; the measured half is
// media_attributes/media_object, populated by a probe at import, stage 5). Scoped to
// what music release names actually carry -- video fields (resolution, codec, HDR)
// have no music equivalent and are not anticipated here.
type Attributes struct {
	// Format is the audio codec/container a release name names: "flac", "mp3",
	// "aac", "alac", "ogg", "wav" -- empty when the name says nothing about it.
	Format string
	// Lossless is derived from Format, not asserted independently, so the two can
	// never disagree.
	Lossless bool
	// BitrateKbps is 0 when unknown, when Lossless makes it meaningless, or when
	// BitrateMode is "vbr" without a named preset -- a fixed number and "variable"
	// are different claims, kept in separate fields rather than the negative-value
	// sentinel this package used before todo 4.2 added BitrateMode.
	BitrateKbps int
	// BitrateMode is "cbr" or "vbr" when the name states or implies one, empty
	// otherwise. A bare numeric claim ("-320-") implies "cbr"; a named preset
	// ("V0", "V2") implies "vbr".
	BitrateMode string
	// VBRPreset is the literal preset token ("V0", "V2") when BitrateMode is
	// "vbr" and the release named one -- empty for a bare "VBR" claim with no
	// preset, or when BitrateMode is not "vbr" at all.
	VBRPreset string
	// SampleRate in Hz, 0 when unstated. Lidarr's own SampleSizeRegex has no
	// sample-rate concept at all (ADR-18's port expands the model); a bare
	// "96KHZ"/"44.1KHZ" token was previously recognised and discarded, not
	// recorded -- todo 4.2 gives it a field to land on.
	SampleRate int
	// Container is empty unless a release name states one distinct from its
	// codec (e.g. an M4A container around an AAC or ALAC stream) -- todo 4.2's
	// fix for "m4a" previously being misclassified as a codec in Format.
	Container string
	// Source is where the release claims to originate: "cd", "web", "vinyl",
	// "cassette" -- empty when unstated.
	Source string
	// Edition is a free-form marker a release name carried: "deluxe",
	// "remastered", "anniversary" -- empty when none was found.
	Edition string
	// ReleaseGroup is the scene/P2P group credited in the name, if any.
	ReleaseGroup string
	// Discs is 0 for an unstated (implicitly single-disc) release.
	Discs int
	// BitDepth is 0 when unstated. ADR-18's parser port expands Lidarr's
	// 24-bit-only detection to whatever depth the name actually claims.
	BitDepth int
	// ProperRepack mirrors media_attributes.proper_repack's measured-side
	// namesake (ADR-39): the release name flags itself a corrected re-release
	// ("PROPER", "REPACK", "REAL", or an explicit version bump).
	ProperRepack bool
}
