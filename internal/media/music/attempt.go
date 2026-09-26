package music

// Passes a file's identification runs through, in order, as its attempt record names them.
const (
	PassEmbeddedTags   = "embedded_tags"
	PassAlbumMatch     = "album_match"
	PassTextSearch     = "text_search"
	PassFingerprint    = "fingerprint"
	PassFolderSiblings = "folder_siblings"
)

// What a pass concluded. Album matching records its own AlbumOutcome instead.
const (
	PassOutcomeIdentified          = "identified"
	PassOutcomeNone                = "none"
	PassOutcomeFailed              = "failed"
	PassOutcomeJoined              = "joined"
	PassOutcomeSiblingsDisagree    = "siblings_disagree"
	PassOutcomeNoIdentifiedSibling = "no_identified_sibling"
	// PassOutcomeRetired is an embedded release group no source knows any more; the album is matched instead.
	PassOutcomeRetired = "retired"
	// PassOutcomeUnplaced is a file the identified album has no track for; later passes decide it.
	PassOutcomeUnplaced = "unplaced"
	// PassOutcomeDisabled is recorded rather than skipped silently, so the files
	// an absent AcoustID key cost can be found once a key is entered (D12).
	PassOutcomeDisabled = "disabled_no_key"
)

// What a whole run ended in.
const (
	AttemptIdentified = "identified"
	AttemptQueued     = "queued"
	AttemptRetry      = "retry"
)
