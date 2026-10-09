package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

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

// NewReleaseCandidate is what Upsert needs to record or refresh one release.
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

// Candidates is section 5.4's own repository: candidate and candidate_decision,
// scaffolded well ahead of stage 5 (migration 0005) and unwired until now.
type Candidates struct{ store *store.Store }

// NewCandidates builds it over an open library store.
func NewCandidates(opened *store.Store) Candidates { return Candidates{store: opened} }

// Upsert records or refreshes one release, keyed on (indexer_id, guid) --
// a repeated search re-finding the same release updates its own claimed
// facts (seeders, size, published_at) rather than piling up duplicates the
// table's own UNIQUE constraint would refuse anyway.
func (c Candidates) Upsert(ctx context.Context, req NewReleaseCandidate) (ReleaseCandidate, error) {
	var row librarydb.Candidate
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
			IndexerID: req.IndexerID, Guid: req.GUID, Title: req.Title,
			SizeBytes: nullInt64(req.SizeBytes), Seeders: nullInt64(req.Seeders),
			Leechers: nullInt64(req.Leechers), PublishedAt: nullInt64(req.PublishedAt),
			Protocol: req.Protocol, DownloadUrl: req.DownloadURL,
			ParseConfidence: nullFloat64(req.ParseConfidence), Source: req.Source,
			FirstSeenAt: req.FirstSeenAt, ExpiresAt: req.ExpiresAt,
		})
		return writeErr
	})
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("library: upsert candidate: %w", err)
	}
	return releaseCandidateFromRow(row), nil
}

// Get answers one release by id.
func (c Candidates) Get(ctx context.Context, id int64) (ReleaseCandidate, error) {
	row, err := librarydb.New(c.store.Read()).GetCandidate(ctx, id)
	if err != nil {
		return ReleaseCandidate{}, fmt.Errorf("library: get candidate %d: %w", id, err)
	}
	return releaseCandidateFromRow(row), nil
}

// RecordDecision persists one candidate_decision row.
func (c Candidates) RecordDecision(ctx context.Context, req NewDecision) (Decision, error) {
	var row librarydb.CandidateDecision
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).RecordDecision(ctx, librarydb.RecordDecisionParams{
			CandidateID: req.CandidateID, DesiredItemID: req.DesiredItemID, Outcome: req.Outcome,
			Score: req.Score, ComponentsJson: req.ComponentsJSON,
			RejectionsJson: nullString(req.RejectionsJSON), DeferralsJson: nullString(req.DeferralsJSON),
			MatchConfidence: nullFloat64(req.MatchConfidence), DecidedAt: req.DecidedAt,
		})
		return writeErr
	})
	if err != nil {
		return Decision{}, fmt.Errorf("library: record decision: %w", err)
	}
	return decisionFromRow(row), nil
}

// ListDecisionsForItem answers every decision recorded against one desired
// item, its own release alongside it, newest first (F1, F16) -- "why wasn't
// this downloaded" (section 12) is this query, one join, no history.db.
func (c Candidates) ListDecisionsForItem(ctx context.Context, itemID int64, limit int64) ([]DecisionDetail, error) {
	rows, err := librarydb.New(c.store.Read()).ListDecisionsForItemWithCandidate(ctx,
		librarydb.ListDecisionsForItemWithCandidateParams{DesiredItemID: itemID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("library: list decisions for item %d: %w", itemID, err)
	}
	details := make([]DecisionDetail, len(rows))
	for i, row := range rows {
		details[i] = DecisionDetail{
			Decision: Decision{
				ID: row.ID, CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID,
				Outcome: row.Outcome, Score: row.Score, ComponentsJSON: row.ComponentsJson,
				RejectionsJSON: row.RejectionsJson.String, DeferralsJSON: row.DeferralsJson.String,
				MatchConfidence: nullFloat64Ptr(row.MatchConfidence), DecidedAt: row.DecidedAt,
			},
			ReleaseCandidate: ReleaseCandidate{
				ID: row.CandidateID, IndexerID: row.IndexerID, GUID: row.Guid, Title: row.Title,
				SizeBytes: int64Ptr(row.SizeBytes), Seeders: int64Ptr(row.Seeders), Leechers: int64Ptr(row.Leechers),
				PublishedAt: int64Ptr(row.PublishedAt), Protocol: row.Protocol, DownloadURL: row.DownloadUrl,
				ParseConfidence: nullFloat64Ptr(row.ParseConfidence), Source: row.Source,
				FirstSeenAt: row.FirstSeenAt, ExpiresAt: row.ExpiresAt,
			},
		}
	}
	return details, nil
}

func releaseCandidateFromRow(row librarydb.Candidate) ReleaseCandidate {
	return ReleaseCandidate{
		ID: row.ID, IndexerID: row.IndexerID, GUID: row.Guid, Title: row.Title,
		SizeBytes: int64Ptr(row.SizeBytes), Seeders: int64Ptr(row.Seeders), Leechers: int64Ptr(row.Leechers),
		PublishedAt: int64Ptr(row.PublishedAt), Protocol: row.Protocol, DownloadURL: row.DownloadUrl,
		ParseConfidence: nullFloat64Ptr(row.ParseConfidence), Source: row.Source,
		FirstSeenAt: row.FirstSeenAt, ExpiresAt: row.ExpiresAt,
	}
}

func decisionFromRow(row librarydb.CandidateDecision) Decision {
	return Decision{
		ID: row.ID, CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID,
		Outcome: row.Outcome, Score: row.Score, ComponentsJSON: row.ComponentsJson,
		RejectionsJSON: row.RejectionsJson.String, DeferralsJSON: row.DeferralsJson.String,
		MatchConfidence: nullFloat64Ptr(row.MatchConfidence), DecidedAt: row.DecidedAt,
	}
}

// nullFloat64 converts an optional float64 to sql.NullFloat64, the same
// shape nullInt64/nullString already give the rest of this package.
func nullFloat64(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

// nullFloat64Ptr is nullFloat64's inverse, matching int64Ptr's own shape.
func nullFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	value := v.Float64
	return &value
}

// DecisionRollup is one aged-decision count: an item, a day and an outcome.
type DecisionRollup struct {
	DesiredItemID, Period int64
	Outcome               string
	Count                 int64
	BestScore             *float64
	TopReason             string
}

// CandidatesAfter answers up to limit releases after id afterID, oldest first: what statistics.db's copy reads.
func (c Candidates) CandidatesAfter(ctx context.Context, afterID, limit int64) ([]ReleaseCandidate, error) {
	rows, err := librarydb.New(c.store.Read()).ListCandidatesAfter(ctx, librarydb.ListCandidatesAfterParams{ID: afterID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("library: candidates after %d: %w", afterID, err)
	}
	out := make([]ReleaseCandidate, len(rows))
	for i, row := range rows {
		out[i] = releaseCandidateFromRow(row)
	}
	return out, nil
}

// DecisionsAfter answers up to limit decisions after id afterID, oldest first, each with its item's library.
func (c Candidates) DecisionsAfter(ctx context.Context, afterID, limit int64) ([]Decision, error) {
	rows, err := librarydb.New(c.store.Read()).ListDecisionsAfter(ctx, librarydb.ListDecisionsAfterParams{ID: afterID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("library: decisions after %d: %w", afterID, err)
	}
	out := make([]Decision, len(rows))
	for i, row := range rows {
		out[i] = Decision{
			ID: row.ID, CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID, CategoryID: row.CategoryID,
			Outcome: row.Outcome, Score: row.Score, ComponentsJSON: row.ComponentsJson,
			RejectionsJSON: row.RejectionsJson.String, DeferralsJSON: row.DeferralsJson.String,
			MatchConfidence: nullFloat64Ptr(row.MatchConfidence), DecidedAt: row.DecidedAt,
		}
	}
	return out, nil
}

// DecisionRollups answers every aged-decision count library.db holds.
func (c Candidates) DecisionRollups(ctx context.Context) ([]DecisionRollup, error) {
	rows, err := librarydb.New(c.store.Read()).ListAllDecisionRollup(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: decision rollups: %w", err)
	}
	out := make([]DecisionRollup, len(rows))
	for i, row := range rows {
		out[i] = DecisionRollup{
			DesiredItemID: row.DesiredItemID, Period: row.Period, Outcome: row.Outcome, Count: row.Count,
			BestScore: nullFloat64Ptr(row.BestScore), TopReason: row.TopReason.String,
		}
	}
	return out, nil
}
