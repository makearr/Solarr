package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// TranscodeEntry is the transcode configuration in the export; libraries are keyed media_type/name and setups by their target's media_type/name because ids are not portable.
type TranscodeEntry struct {
	Enabled             bool                    `yaml:"enabled"`
	Parallel            LaneLimitsEntry         `yaml:"parallel"`
	PlaybackParallel    LaneLimitsEntry         `yaml:"playback_parallel"`
	OriginalsRetainDays int64                   `yaml:"originals_retain_days"`
	OriginalsCapGB      int64                   `yaml:"originals_cap_gb"`
	Libraries           []TranscodeLibraryEntry `yaml:"libraries,omitempty"`
	Setups              []TranscodeSetupEntry   `yaml:"setups,omitempty"`
}

// LaneLimitsEntry is LaneLimits in the export.
type LaneLimitsEntry struct {
	CPU   int64 `yaml:"cpu"`
	GPU   int64 `yaml:"gpu"`
	Audio int64 `yaml:"audio"`
	Comic int64 `yaml:"comic"`
	Ebook int64 `yaml:"ebook"`
}

// TranscodeLibraryEntry is one library's transcode row in the export.
type TranscodeLibraryEntry struct {
	Library  string         `yaml:"library"`
	Enabled  bool           `yaml:"enabled"`
	Priority int64          `yaml:"priority"`
	Order    TranscodeOrder `yaml:"order"`
}

// TranscodeSetupEntry is one saved setup in the export; the document is carried as a generic map so the YAML reads like the API's JSON.
type TranscodeSetupEntry struct {
	Target string         `yaml:"target"`
	Setup  map[string]any `yaml:"setup"`
}

// ExportTranscode fills the document's transcode section from what is saved.
func (c *Cache) ExportTranscode(ctx context.Context, document *Document) error {
	snapshot := c.Snapshot()
	global := snapshot.TranscodeGlobal()
	entry := &TranscodeEntry{
		Enabled: global.Enabled, Parallel: LaneLimitsEntry(global.Parallel), PlaybackParallel: LaneLimitsEntry(global.PlaybackParallel),
		OriginalsRetainDays: global.Originals.RetainDays, OriginalsCapGB: global.Originals.CapGB,
	}
	rows, err := c.CategoryTranscodes(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if category, found := snapshot.Category(row.CategoryID); found {
			entry.Libraries = append(entry.Libraries, TranscodeLibraryEntry{
				Library: category.MediaType + "/" + category.Name, Enabled: row.Enabled, Priority: row.Priority, Order: row.Order,
			})
		}
	}
	revisions, err := c.ListTranscodeSetupRevisions(ctx)
	if err != nil {
		return err
	}
	for _, target := range snapshot.QualityTargets() {
		if _, saved := revisions[target.ID]; !saved {
			continue
		}
		stored, err := c.TranscodeSetup(ctx, target.ID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(stored.Setup)
		if err != nil {
			return fmt.Errorf("settings: export transcode setup: %w", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(raw, &generic); err != nil {
			return fmt.Errorf("settings: export transcode setup: %w", err)
		}
		entry.Setups = append(entry.Setups, TranscodeSetupEntry{Target: target.MediaType + "/" + target.Name, Setup: generic})
	}
	document.Transcode = entry
	return nil
}

// ImportTranscode applies the document's transcode section; a library or target the instance lacks is skipped, a setup that does not validate refuses the whole section before anything is written.
func (c *Cache) ImportTranscode(ctx context.Context, cl clock.Clock, document Document) error {
	if document.Transcode == nil {
		return nil
	}
	entry := document.Transcode
	snapshot := c.Snapshot()
	targetByKey := map[string]QualityTarget{}
	for _, target := range snapshot.QualityTargets() {
		targetByKey[target.MediaType+"/"+target.Name] = target
	}
	type parsedSetup struct {
		target QualityTarget
		setup  TranscodeSetup
	}
	var setups []parsedSetup
	var problems config.Problems
	for index, one := range entry.Setups {
		target, found := targetByKey[one.Target]
		if !found {
			continue
		}
		where := fmt.Sprintf("transcode.setups[%d]", index)
		raw, err := json.Marshal(one.Setup)
		if err == nil {
			var setup TranscodeSetup
			if setup, err = ParseTranscodeSetup(raw, target); err == nil {
				for _, problem := range setup.Validate(target) {
					problem.Path = where + "." + problem.Path
					problems = append(problems, problem)
				}
				setups = append(setups, parsedSetup{target: target, setup: setup})
				continue
			}
		}
		problems = append(problems, config.Problem{Path: where, Value: one.Target, Expectation: "a transcode setup document: " + err.Error()})
	}
	if len(problems) > 0 {
		return problems
	}
	global := TranscodeGlobal{
		Enabled: entry.Enabled, Parallel: LaneLimits(entry.Parallel), PlaybackParallel: LaneLimits(entry.PlaybackParallel),
		Originals: OriginalsPolicy{RetainDays: entry.OriginalsRetainDays, CapGB: entry.OriginalsCapGB},
	}
	if err := c.SetTranscodeGlobal(ctx, cl, global); err != nil {
		return err
	}
	idByKey := map[string]int64{}
	for _, category := range snapshot.Categories() {
		idByKey[category.MediaType+"/"+category.Name] = category.ID
	}
	for _, library := range entry.Libraries {
		id, found := idByKey[library.Library]
		if !found {
			continue
		}
		if err := c.SetCategoryTranscode(ctx, CategoryTranscode{CategoryID: id, Enabled: library.Enabled, Priority: library.Priority, Order: library.Order}); err != nil {
			return fmt.Errorf("transcode library %q: %w", library.Library, err)
		}
	}
	for _, one := range setups {
		if err := c.SetTranscodeSetup(ctx, cl, one.target.ID, one.setup); err != nil {
			return fmt.Errorf("transcode setup %q: %w", one.target.Name, err)
		}
	}
	return nil
}
