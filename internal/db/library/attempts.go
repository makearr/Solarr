package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
)

// attemptsKept is how many identification runs per file are kept.
const attemptsKept = 5

// Attempts is the record of identification runs (migration 0033).
type Attempts struct{ store *store.Store }

// NewAttempts builds it over an open library store.
func NewAttempts(opened *store.Store) Attempts { return Attempts{store: opened} }

// Attempt is one identification run for one file.
type Attempt struct {
	MediaObjectID int64
	At            int64
	// Outcome is what the run ended in: identified, queued, or retry after a lookup failure.
	Outcome string
	Passes  []AttemptPass
}

// AttemptPass is one pass of a run: what it concluded and what it weighed.
type AttemptPass struct {
	Pass       string             `json:"pass"`
	Outcome    string             `json:"outcome"`
	Candidates []AttemptCandidate `json:"candidates,omitempty"`
}

// AttemptCandidate is one candidate a pass weighed, with the structured evidence for its verdict.
type AttemptCandidate struct {
	Strategy   string            `json:"strategy"`
	Provider   string            `json:"provider,omitempty"`
	ExternalID string            `json:"external_id,omitempty"`
	Confidence float64           `json:"confidence"`
	Evidence   []AttemptEvidence `json:"evidence,omitempty"`
}

// AttemptEvidence is one fact about a candidate (ADR-10: data, never a sentence).
type AttemptEvidence struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// AttemptCandidatesFrom keeps what a manual decision needs from a chain's identifications.
func AttemptCandidatesFrom(identifications []model.Identification) []AttemptCandidate {
	candidates := make([]AttemptCandidate, len(identifications))
	for i, identification := range identifications {
		candidate := AttemptCandidate{Strategy: identification.Strategy, Confidence: identification.Confidence}
		if identification.ExternalID != nil {
			candidate.Provider, candidate.ExternalID = identification.ExternalID.Provider, identification.ExternalID.ExternalID
		}
		for _, evidence := range identification.Evidence {
			candidate.Evidence = append(candidate.Evidence, AttemptEvidence{Kind: evidence.Kind, Detail: evidence.Detail})
		}
		candidates[i] = candidate
	}
	return candidates
}

// Record stores one batch's runs in one transaction, keeping the newest attemptsKept per file.
func (a Attempts) Record(ctx context.Context, attempts []Attempt) error {
	if len(attempts) == 0 {
		return nil
	}
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, attempt := range attempts {
			passes, err := json.Marshal(attempt.Passes)
			if err != nil {
				return fmt.Errorf("library: encode attempt for media object %d: %w", attempt.MediaObjectID, err)
			}
			if err := queries.InsertIdentificationAttempt(ctx, librarydb.InsertIdentificationAttemptParams{
				MediaObjectID: attempt.MediaObjectID, AttemptedAt: attempt.At, Outcome: attempt.Outcome, PassesJson: string(passes),
			}); err != nil {
				return fmt.Errorf("library: record attempt for media object %d: %w", attempt.MediaObjectID, err)
			}
			if err := queries.PruneIdentificationAttempts(ctx, librarydb.PruneIdentificationAttemptsParams{
				MediaObjectID: attempt.MediaObjectID, Keep: attemptsKept,
			}); err != nil {
				return fmt.Errorf("library: prune attempts for media object %d: %w", attempt.MediaObjectID, err)
			}
		}
		return nil
	})
}

// List answers one file's runs, newest first.
func (a Attempts) List(ctx context.Context, mediaObjectID int64) ([]Attempt, error) {
	rows, err := librarydb.New(a.store.Read()).ListIdentificationAttempts(ctx, mediaObjectID)
	if err != nil {
		return nil, fmt.Errorf("library: list attempts for media object %d: %w", mediaObjectID, err)
	}
	attempts := make([]Attempt, len(rows))
	for i, row := range rows {
		attempts[i] = Attempt{MediaObjectID: row.MediaObjectID, At: row.AttemptedAt, Outcome: row.Outcome}
		if err := json.Unmarshal([]byte(row.PassesJson), &attempts[i].Passes); err != nil {
			return nil, fmt.Errorf("library: decode attempt %d: %w", row.ID, err)
		}
	}
	return attempts, nil
}

// SkippedFile is one queued file whose newest identification run recorded the
// pass and outcome asked for, with the directory a targeted rescan needs.
type SkippedFile struct {
	MediaObjectID int64
	RootFolderID  int64
	// Directory is rel_path's own parent, which is what scan.Payload.Directories
	// takes -- a rescan works over directories, never single files.
	Directory string
}

// ListSkipped answers the queued files whose newest run recorded pass/outcome,
// D12's "find what a missing AcoustID key cost once a key is entered". Paged on
// media_object_id (ADR-22: a cursor, never an OFFSET); the caller pages until it
// answers fewer than pageSize. The pass filter is applied here rather than in SQL
// because sqlc's SQLite parser cannot model json_each's columns in a WHERE.
func (a Attempts) ListSkipped(
	ctx context.Context, pass, outcome string, afterID int64, pageSize int,
) (files []SkippedFile, lastID int64, err error) {
	rows, err := librarydb.New(a.store.Read()).ListQueuedWithNewestAttempt(ctx,
		librarydb.ListQueuedWithNewestAttemptParams{AfterID: afterID, PageSize: int64(pageSize)})
	if err != nil {
		return nil, afterID, fmt.Errorf("library: list queued files with their newest attempt: %w", err)
	}
	lastID = afterID
	for _, row := range rows {
		lastID = row.MediaObjectID
		var passes []AttemptPass
		if err := json.Unmarshal([]byte(row.PassesJson), &passes); err != nil {
			return nil, lastID, fmt.Errorf("library: decode attempt for media object %d: %w", row.MediaObjectID, err)
		}
		if !slices.ContainsFunc(passes, func(p AttemptPass) bool { return p.Pass == pass && p.Outcome == outcome }) {
			continue
		}
		files = append(files, SkippedFile{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID,
			Directory: path.Dir(row.RelPath),
		})
	}
	return files, lastID, nil
}
