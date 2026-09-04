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
	// BitrateKbps is 0 when unknown or when Lossless makes it meaningless.
	BitrateKbps int
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
