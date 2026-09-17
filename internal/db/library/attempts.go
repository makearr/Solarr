package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/identify/model"
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
