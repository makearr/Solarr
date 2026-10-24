package settings

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// KeyImportTransferLadder is the ordered list of transfer methods import walks
// until one succeeds, tier normal: which of them placed a file is invisible on
// disk, so the order has to be stated rather than inferred (2026-09-18).
const KeyImportTransferLadder = "import.transfer_ladder"

// The transfer methods a ladder may name. These pair one-for-one with
// mover.PlaceMethod, which this package may not import (ADR-25 layering);
// music.Committer maps between them.
const (
	TransferReflink  = "reflink"
	TransferHardlink = "hardlink"
	TransferMove     = "move"
	TransferCopy     = "copy"
	TransferSymlink  = "symlink"
)

// DefaultImportTransferLadder is the order the user gave on 2026-09-18, symlink dropped 2026-09-30 (ADR-11).
// Mirrors mover.DefaultLadder, which this package may not import.
var DefaultImportTransferLadder = []string{TransferReflink, TransferHardlink, TransferMove, TransferCopy}

// ValidTransferMethod reports whether name is one of the five known methods.
func ValidTransferMethod(name string) bool {
	switch name {
	case TransferReflink, TransferHardlink, TransferMove, TransferCopy, TransferSymlink:
		return true
	default:
		return false
	}
}

// ImportTransferLadder reads the configured order, falling back to the shipped
// default when nothing is stored -- an absent row means "use the default
// ladder", never "place nothing".
func (s *Snapshot) ImportTransferLadder() []string {
	ladder := s.policyStringList(KeyImportTransferLadder)
	if len(ladder) == 0 {
		return DefaultImportTransferLadder
	}
	return ladder
}

// SetImportTransferLadder writes the order (ADR-44: validated on write, since
// an unknown or empty ladder would fail every future import at placement time).
func (c *Cache) SetImportTransferLadder(ctx context.Context, cl clock.Clock, ladder []string) error {
	if problems := validateImportTransferLadder(ladder); len(problems) > 0 {
		return problems
	}
	encoded, err := json.Marshal(ladder)
	if err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
			Key: KeyImportTransferLadder, ValueJson: string(encoded), UpdatedAt: cl.Now().Unix(),
		})
	})
}

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
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || parsed > 1 {
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

// KeyImportRenameOnImport is Lidarr's own "rename tracks" switch at the
// import boundary, tier normal: on, an imported file is named by the
// category's template; off, it keeps the name it arrived with.
const KeyImportRenameOnImport = "import.rename_on_import"

// DefaultImportRenameOnImport is on, the shape every shipped naming template
// assumes (2026-09-19).
const DefaultImportRenameOnImport = true

// ImportRenameOnImport reads the switch, defaulting to on when nothing is
// stored -- an absent row means "name it by the template", never "keep
// whatever the indexer called it".
func (s *Snapshot) ImportRenameOnImport() bool {
	return s.policyBool(KeyImportRenameOnImport, DefaultImportRenameOnImport)
}

// SetImportRenameOnImport writes the switch.
func (c *Cache) SetImportRenameOnImport(ctx context.Context, cl clock.Clock, rename bool) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
			Key: KeyImportRenameOnImport, ValueJson: boolPolicy(rename), UpdatedAt: cl.Now().Unix(),
		})
	})
}

// UpdateImportSettings validates and writes the supplied import policy values
// in one transaction. A nil value leaves that setting untouched.
func (c *Cache) UpdateImportSettings(ctx context.Context, cl clock.Clock, confidence *float64, ladder *[]string, rename *bool) error {
	if confidence != nil {
		if problems := validateImportAutoCommitConfidence(*confidence); len(problems) > 0 {
			return problems
		}
	}
	var encodedLadder []byte
	if ladder != nil {
		if problems := validateImportTransferLadder(*ladder); len(problems) > 0 {
			return problems
		}
		var err error
		encodedLadder, err = json.Marshal(*ladder)
		if err != nil {
			return err
		}
	}
	if confidence == nil && ladder == nil && rename == nil {
		return nil
	}
	updatedAt := cl.Now().Unix()
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if confidence != nil {
			if err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: KeyImportAutoCommitConfidence, ValueJson: strconv.FormatFloat(*confidence, 'f', -1, 64), UpdatedAt: updatedAt,
			}); err != nil {
				return err
			}
		}
		if ladder != nil {
			if err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: KeyImportTransferLadder, ValueJson: string(encodedLadder), UpdatedAt: updatedAt,
			}); err != nil {
				return err
			}
		}
		if rename != nil {
			if err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
				Key: KeyImportRenameOnImport, ValueJson: boolPolicy(*rename), UpdatedAt: updatedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
