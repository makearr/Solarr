package statistics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// retentionPage is Invariant 2's bulk chunk: how many decisions or candidates one transaction retires or copies.
const retentionPage = 200

// secondsPerDay sizes decision_rollup's day bucket.
const secondsPerDay = 86_400

// Candidates is the releases searches found and what was decided about each, for "why wasn't this downloaded"
// (section 12) and a person's own pick; nothing that decides reads them, a held release carries its own copy.
type Candidates struct{ store *store.Store }

// NewCandidates builds it over an open statistics store.
func NewCandidates(opened *store.Store) Candidates { return Candidates{store: opened} }

// Upsert records or refreshes one release, keyed on (indexer, guid): a repeated search updates its claimed facts.
func (c Candidates) Upsert(ctx context.Context, req library.NewReleaseCandidate) (library.ReleaseCandidate, error) {
	var row statisticsdb.Candidate
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		row, err = statisticsdb.New(tx).UpsertCandidate(ctx, statisticsdb.UpsertCandidateParams{
			IndexerID: req.IndexerID, Guid: req.GUID, Title: req.Title,
			SizeBytes: nullInt64(req.SizeBytes), Seeders: nullInt64(req.Seeders), Leechers: nullInt64(req.Leechers),
			PublishedAt: nullInt64(req.PublishedAt), Protocol: req.Protocol, DownloadUrl: req.DownloadURL,
			ParseConfidence: nullFloat64(req.ParseConfidence), Source: req.Source,
			FirstSeenAt: req.FirstSeenAt, ExpiresAt: req.ExpiresAt,
		})
		return err
	})
	if err != nil {
		return library.ReleaseCandidate{}, fmt.Errorf("statistics: upsert candidate: %w", err)
	}
	return releaseFromRow(row), nil
}

// Get answers one release by id.
func (c Candidates) Get(ctx context.Context, id int64) (library.ReleaseCandidate, error) {
	row, err := statisticsdb.New(c.store.Read()).GetCandidate(ctx, id)
	if err != nil {
		return library.ReleaseCandidate{}, fmt.Errorf("statistics: get candidate %d: %w", id, err)
	}
	return releaseFromRow(row), nil
}

// RecordDecision persists one decision, with the item's library beside it.
func (c Candidates) RecordDecision(ctx context.Context, req library.NewDecision) (library.Decision, error) {
	var row statisticsdb.CandidateDecision
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		row, err = statisticsdb.New(tx).RecordDecision(ctx, statisticsdb.RecordDecisionParams{
			CandidateID: req.CandidateID, DesiredItemID: req.DesiredItemID, CategoryID: req.CategoryID, Outcome: req.Outcome,
			Score: req.Score, ComponentsJson: req.ComponentsJSON,
			RejectionsJson: nullString(req.RejectionsJSON), DeferralsJson: nullString(req.DeferralsJSON),
			MatchConfidence: nullFloat64(req.MatchConfidence), DecidedAt: req.DecidedAt,
		})
		return err
	})
	if err != nil {
		return library.Decision{}, fmt.Errorf("statistics: record decision: %w", err)
	}
	return library.Decision{
		ID: row.ID, CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID, CategoryID: row.CategoryID,
		Outcome: row.Outcome, Score: row.Score, ComponentsJSON: row.ComponentsJson,
		RejectionsJSON: row.RejectionsJson.String, DeferralsJSON: row.DeferralsJson.String,
		MatchConfidence: floatPtr(row.MatchConfidence), DecidedAt: row.DecidedAt,
	}, nil
}

// ListDecisionsForItem answers the decisions recorded against one item, each with its release, newest first (F1, F16).
func (c Candidates) ListDecisionsForItem(ctx context.Context, itemID, limit int64) ([]library.DecisionDetail, error) {
	rows, err := statisticsdb.New(c.store.Read()).ListDecisionsForItemWithCandidate(ctx,
		statisticsdb.ListDecisionsForItemWithCandidateParams{DesiredItemID: itemID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("statistics: list decisions for item %d: %w", itemID, err)
	}
	details := make([]library.DecisionDetail, len(rows))
	for i, row := range rows {
		details[i] = library.DecisionDetail{
			Decision: library.Decision{
				ID: row.ID, CandidateID: row.CandidateID, DesiredItemID: row.DesiredItemID,
				Outcome: row.Outcome, Score: row.Score, ComponentsJSON: row.ComponentsJson,
				RejectionsJSON: row.RejectionsJson.String, DeferralsJSON: row.DeferralsJson.String,
				MatchConfidence: floatPtr(row.MatchConfidence), DecidedAt: row.DecidedAt,
			},
			ReleaseCandidate: library.ReleaseCandidate{
				ID: row.CandidateID, IndexerID: row.IndexerID, GUID: row.Guid, Title: row.Title,
				SizeBytes: intPtr(row.SizeBytes), Seeders: intPtr(row.Seeders), Leechers: intPtr(row.Leechers),
				PublishedAt: intPtr(row.PublishedAt), Protocol: row.Protocol, DownloadURL: row.DownloadUrl,
				ParseConfidence: floatPtr(row.ParseConfidence), Source: row.Source,
				FirstSeenAt: row.FirstSeenAt, ExpiresAt: row.ExpiresAt,
			},
		}
	}
	return details, nil
}

// DecisionsByOutcome counts the decisions since a time, per library and outcome, for the statistics.
func (c Candidates) DecisionsByOutcome(ctx context.Context, since int64) ([]Figure, error) {
	rows, err := statisticsdb.New(c.store.Read()).AggregateDecisionsByCategoryAndOutcome(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("statistics: decisions by outcome: %w", err)
	}
	figures := make([]Figure, len(rows))
	for i, row := range rows {
		figures[i] = Figure{
			Metric: library.StatDecisionsByOutcome, Dimension: Dimension(row.CategoryID, row.Outcome),
			Value: float64(row.Decisions), Count: row.Decisions,
		}
	}
	return figures, nil
}

// Retired counts what one retention pass removed.
type Retired struct {
	Decisions  int64
	Candidates int64
}

// rollupKey is one decision_rollup row: an item, a day and an outcome.
type rollupKey struct {
	itemID  int64
	period  int64
	outcome string
}

// rollupSum is what a page of decisions adds to one rollup row.
type rollupSum struct {
	count   int64
	best    float64
	reasons map[string]int
}

// Retire ages decisions older than decisionsBefore into decision_rollup and deletes them, then deletes the
// candidates that expired by now and that no decision refers to any more, in pages, at most maxPages of each.
func (c Candidates) Retire(ctx context.Context, now, decisionsBefore time.Time, maxPages int) (Retired, error) {
	var retired Retired
	for range maxPages {
		removed, err := c.retireDecisionPage(ctx, decisionsBefore.Unix())
		if err != nil {
			return retired, err
		}
		retired.Decisions += removed
		if removed == 0 {
			break
		}
	}
	for range maxPages {
		var removed int64
		err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			removed, err = statisticsdb.New(tx).DeleteUnreferencedExpiredCandidates(ctx,
				statisticsdb.DeleteUnreferencedExpiredCandidatesParams{Now: now.Unix(), PageSize: retentionPage})
			return err
		})
		if err != nil {
			return retired, fmt.Errorf("statistics: retire candidates: %w", err)
		}
		retired.Candidates += removed
		if removed < retentionPage {
			break
		}
	}
	return retired, nil
}

// retireDecisionPage rolls the oldest page's aged decisions up and deletes them in one transaction, so a decision is
// never counted twice or lost between the two.
func (c Candidates) retireDecisionPage(ctx context.Context, decidedBefore int64) (int64, error) {
	rows, err := statisticsdb.New(c.store.Read()).ListDecisionsToRetire(ctx,
		statisticsdb.ListDecisionsToRetireParams{PageSize: retentionPage, DecidedBefore: decidedBefore})
	if err != nil {
		return 0, fmt.Errorf("statistics: list decisions to retire: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	sums := make(map[rollupKey]*rollupSum)
	var order []rollupKey
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		key := rollupKey{itemID: row.DesiredItemID, period: row.DecidedAt / secondsPerDay, outcome: row.Outcome}
		sum, seen := sums[key]
		if !seen {
			sum = &rollupSum{best: row.Score, reasons: make(map[string]int)}
			sums[key] = sum
			order = append(order, key)
		}
		sum.count++
		sum.best = max(sum.best, row.Score)
		if reason := firstRejectionRule(row.RejectionsJson); reason != "" {
			sum.reasons[reason]++
		}
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return 0, fmt.Errorf("statistics: encode decision ids: %w", err)
	}
	var removed int64
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		for _, key := range order {
			sum := sums[key]
			reason := mostFrequent(sum.reasons)
			if err := queries.RollUpDecisions(ctx, statisticsdb.RollUpDecisionsParams{
				DesiredItemID: key.itemID, Period: key.period, Outcome: key.outcome, Count: sum.count,
				BestScore: sql.NullFloat64{Float64: sum.best, Valid: true},
				TopReason: sql.NullString{String: reason, Valid: reason != ""},
			}); err != nil {
				return fmt.Errorf("roll up: %w", err)
			}
		}
		var err error
		removed, err = queries.DeleteDecisionsByID(ctx, string(encoded))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("statistics: retire decisions: %w", err)
	}
	return removed, nil
}

// DecisionRollup answers one item's aged decisions, newest day first.
func (c Candidates) DecisionRollup(ctx context.Context, itemID int64) ([]library.DecisionRollup, error) {
	rows, err := statisticsdb.New(c.store.Read()).ListDecisionRollup(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("statistics: decision rollup of item %d: %w", itemID, err)
	}
	out := make([]library.DecisionRollup, len(rows))
	for i, row := range rows {
		out[i] = library.DecisionRollup{
			DesiredItemID: row.DesiredItemID, Period: row.Period, Outcome: row.Outcome, Count: row.Count,
			BestScore: floatPtr(row.BestScore), TopReason: row.TopReason.String,
		}
	}
	return out, nil
}

// LastCopied answers the newest candidate and decision copied from library.db, where a copy resumes.
func (c Candidates) LastCopied(ctx context.Context) (candidateID, decisionID int64, err error) {
	queries := statisticsdb.New(c.store.Read())
	if candidateID, err = queries.LastCandidateID(ctx); err != nil {
		return 0, 0, fmt.Errorf("statistics: last candidate: %w", err)
	}
	if decisionID, err = queries.LastDecisionID(ctx); err != nil {
		return 0, 0, fmt.Errorf("statistics: last decision: %w", err)
	}
	return candidateID, decisionID, nil
}

// CopyCandidates stores releases from library.db under their own ids; one already copied is left as it is.
func (c Candidates) CopyCandidates(ctx context.Context, releases []library.ReleaseCandidate) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		for _, release := range releases {
			if err := queries.CopyCandidate(ctx, statisticsdb.CopyCandidateParams{
				ID: release.ID, IndexerID: release.IndexerID, Guid: release.GUID, Title: release.Title,
				SizeBytes: nullInt64(release.SizeBytes), Seeders: nullInt64(release.Seeders), Leechers: nullInt64(release.Leechers),
				PublishedAt: nullInt64(release.PublishedAt), Protocol: release.Protocol, DownloadUrl: release.DownloadURL,
				ParseConfidence: nullFloat64(release.ParseConfidence), Source: release.Source,
				FirstSeenAt: release.FirstSeenAt, ExpiresAt: release.ExpiresAt,
			}); err != nil {
				return fmt.Errorf("statistics: copy candidate %d: %w", release.ID, err)
			}
		}
		return nil
	})
}

// CopyDecisions stores decisions from library.db under their own ids; one already copied is left as it is.
func (c Candidates) CopyDecisions(ctx context.Context, decisions []library.Decision) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		for _, decision := range decisions {
			if err := queries.CopyDecision(ctx, statisticsdb.CopyDecisionParams{
				ID: decision.ID, CandidateID: decision.CandidateID, DesiredItemID: decision.DesiredItemID,
				CategoryID: decision.CategoryID, Outcome: decision.Outcome, Score: decision.Score,
				ComponentsJson: decision.ComponentsJSON, RejectionsJson: nullString(decision.RejectionsJSON),
				DeferralsJson: nullString(decision.DeferralsJSON), MatchConfidence: nullFloat64(decision.MatchConfidence),
				DecidedAt: decision.DecidedAt,
			}); err != nil {
				return fmt.Errorf("statistics: copy decision %d: %w", decision.ID, err)
			}
		}
		return nil
	})
}

// CopyDecisionRollups stores library.db's aged-decision counts once: none are copied when any are already here.
func (c Candidates) CopyDecisionRollups(ctx context.Context, rollups []library.DecisionRollup) error {
	return c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		held, err := queries.CountDecisionRollup(ctx)
		if err != nil || held > 0 {
			return err
		}
		for _, rollup := range rollups {
			if err := queries.CopyDecisionRollup(ctx, statisticsdb.CopyDecisionRollupParams{
				DesiredItemID: rollup.DesiredItemID, Period: rollup.Period, Outcome: rollup.Outcome, Count: rollup.Count,
				BestScore: nullFloat64(rollup.BestScore), TopReason: sql.NullString{String: rollup.TopReason, Valid: rollup.TopReason != ""},
			}); err != nil {
				return fmt.Errorf("statistics: copy decision rollup: %w", err)
			}
		}
		return nil
	})
}

func releaseFromRow(row statisticsdb.Candidate) library.ReleaseCandidate {
	return library.ReleaseCandidate{
		ID: row.ID, IndexerID: row.IndexerID, GUID: row.Guid, Title: row.Title,
		SizeBytes: intPtr(row.SizeBytes), Seeders: intPtr(row.Seeders), Leechers: intPtr(row.Leechers),
		PublishedAt: intPtr(row.PublishedAt), Protocol: row.Protocol, DownloadURL: row.DownloadUrl,
		ParseConfidence: floatPtr(row.ParseConfidence), Source: row.Source,
		FirstSeenAt: row.FirstSeenAt, ExpiresAt: row.ExpiresAt,
	}
}

// firstRejectionRule is the rule of a decision's first rejection, the reason it is remembered by.
func firstRejectionRule(raw sql.NullString) string {
	if !raw.Valid || raw.String == "" {
		return ""
	}
	var rejections []struct{ Rule string }
	if err := json.Unmarshal([]byte(raw.String), &rejections); err != nil || len(rejections) == 0 {
		return ""
	}
	return rejections[0].Rule
}

// mostFrequent is the reason counted most often, the alphabetically first among equals so a rerun answers the same.
func mostFrequent(reasons map[string]int) string {
	names := make([]string, 0, len(reasons))
	for name := range reasons {
		names = append(names, name)
	}
	slices.Sort(names)
	best := ""
	for _, name := range names {
		if best == "" || reasons[name] > reasons[best] {
			best = name
		}
	}
	return best
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullFloat64(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

func nullString(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

func intPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	value := v.Int64
	return &value
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	value := v.Float64
	return &value
}
