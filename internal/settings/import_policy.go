package settings

import (
	"context"
	"strconv"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// KeyImportAutoCommitConfidence is ADR-48's single commit-line knob, tier normal:
// the confidence a unit's IDENTIFY result must clear to import automatically
// rather than wait in the manual identification queue (F29).
const KeyImportAutoCommitConfidence = "import.auto_commit_confidence"

// DefaultImportAutoCommitConfidence is ADR-48's own default, named "Confident" in
// its table of five levels.
const DefaultImportAutoCommitConfidence = 0.90

// ImportAutoCommitConfidence reads the configured commit line, or
// DefaultImportAutoCommitConfidence when nothing has been stored yet -- an absent
// policy row means "use the shipped default", not "reject everything".
func (s *Snapshot) ImportAutoCommitConfidence() float64 {
	value, found := s.policy[KeyImportAutoCommitConfidence]
	if !found {
		return DefaultImportAutoCommitConfidence
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return DefaultImportAutoCommitConfidence
	}
	return parsed
}

// SetImportAutoCommitConfidence writes the commit line (ADR-44: validated on
// write, since a bad value here silently changes whether the software acts or
// asks on every future import).
func (c *Cache) SetImportAutoCommitConfidence(ctx context.Context, cl clock.Clock, confidence float64) error {
	if problems := validateImportAutoCommitConfidence(confidence); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
			Key: KeyImportAutoCommitConfidence, ValueJson: strconv.FormatFloat(confidence, 'f', -1, 64),
			UpdatedAt: cl.Now().Unix(),
		})
	})
}
