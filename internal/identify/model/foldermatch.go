package model

// The confidence a folder-to-title resolver reports for the rule that picked a candidate; a placement needs the
// library's import.auto_commit_confidence (0.90 by default), so raising it sends the looser rules to review.
const (
	// FolderExactMatch is the named title (and year or author, where named) found once.
	FolderExactMatch = 1.0
	// FolderSettledMatch is a title found more than once, told apart by the folder's episode numbers.
	FolderSettledMatch = 0.95
	// FolderLooseMatch is a near title, a year off by one, or a name that only continues the folder's.
	FolderLooseMatch = 0.9
)

// AcceptsFolderMatch says a rule's confidence clears the configured threshold; an unset one takes the default 0.9.
func AcceptsFolderMatch(confidence float64, threshold func() float64) bool {
	floor := FolderLooseMatch
	if threshold != nil {
		floor = threshold()
	}
	return confidence >= floor
}
