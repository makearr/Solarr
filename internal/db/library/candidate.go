package library

// ReleaseCandidate is one release an indexer answered, matched against a specific
// desired item's own identity (section 10.1). Claimed only -- there is
// nothing to probe before a download, so AttributesID stays unset; bitrate
// is estimated from size over runtime instead (ADR-39).
type ReleaseCandidate struct {
	ID              int64
	IndexerID       int64
	GUID            string
	Title           string
	SizeBytes       *int64
	Seeders         *int64
	Leechers        *int64
	PublishedAt     *int64
	Protocol        string
	DownloadURL     string
	ParseConfidence *float64
	Source          string
	FirstSeenAt     int64
	ExpiresAt       int64
}

// NewReleaseCandidate is what statistics.Candidates.Upsert needs to record or refresh one release.
type NewReleaseCandidate struct {
	IndexerID       int64
	GUID            string
	Title           string
	SizeBytes       *int64
	Seeders         *int64
	Leechers        *int64
	PublishedAt     *int64
	Protocol        string
	DownloadURL     string
	ParseConfidence *float64
	Source          string
	FirstSeenAt     int64
	ExpiresAt       int64
}

// Decision is one candidate_decision row (ADR-10): the answer to "why (not)
// this release for this desired item", never a formatted verdict.
type Decision struct {
	ID            int64
	CandidateID   int64
	DesiredItemID int64
	// CategoryID is the library the item was in when it was decided; 0 when not known.
	CategoryID      int64
	Outcome         string // "ACCEPT" | "REJECT" | "DEFER" (model.Outcome's own values)
	Score           float64
	ComponentsJSON  string
	RejectionsJSON  string
	DeferralsJSON   string
	MatchConfidence *float64
	DecidedAt       int64
}

// NewDecision is what RecordDecision needs.
type NewDecision struct {
	CandidateID   int64
	DesiredItemID int64
	// CategoryID is the item's library, which statistics.db keeps beside the decision.
	CategoryID      int64
	Outcome         string
	Score           float64
	ComponentsJSON  string
	RejectionsJSON  string
	DeferralsJSON   string
	MatchConfidence *float64
	DecidedAt       int64
}

// DecisionDetail pairs a decision with the release it was decided about --
// F16's own listing needs both; neither means anything alone.
type DecisionDetail struct {
	Decision         Decision
	ReleaseCandidate ReleaseCandidate
}

// DecisionRollup is one aged-decision count: an item, a day and an outcome.
type DecisionRollup struct {
	DesiredItemID, Period int64
	Outcome               string
	Count                 int64
	BestScore             *float64
	TopReason             string
}
