package music

// Passes a file's identification runs through, in order, as its attempt record names them.
const (
	PassEmbeddedTags   = "embedded_tags"
	PassAlbumMatch     = "album_match"
	PassTextSearch     = "text_search"
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
)

// What a whole run ended in.
const (
	AttemptIdentified = "identified"
	AttemptQueued     = "queued"
	AttemptRetry      = "retry"
)
