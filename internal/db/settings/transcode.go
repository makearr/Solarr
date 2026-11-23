package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The instance-wide transcode policy keys (documentation/transcode.md).
const (
	KeyTranscodeEnabled                     = "transcode.enabled"
	KeyTranscodeParallelCPU                 = "transcode.parallel_cpu"
	KeyTranscodeParallelGPU                 = "transcode.parallel_gpu"
	KeyTranscodeParallelAudio               = "transcode.parallel_audio"
	KeyTranscodePlaybackParallelCPU         = "transcode.playback_parallel_cpu"
	KeyTranscodePlaybackParallelGPU         = "transcode.playback_parallel_gpu"
	KeyTranscodePlaybackParallelAudio       = "transcode.playback_parallel_audio"
	KeyTranscodeOriginalsRetainDays         = "transcode.originals_retain_days"
	KeyTranscodeOriginalsCapGB              = "transcode.originals_cap_gb"
	maxTranscodeParallel              int64 = 16
)

// LaneLimits are how many conversions run at once on each kind of work.
type LaneLimits struct{ CPU, GPU, Audio int64 }

// OriginalsPolicy is how long a replaced original stays in the recycle bin (0 days: the bin's own rule) and the most
// space they may take together (0 GB: no cap).
type OriginalsPolicy struct{ RetainDays, CapGB int64 }

// TranscodeGlobal is the instance-wide transcode policy.
type TranscodeGlobal struct {
	Enabled          bool
	Parallel         LaneLimits
	PlaybackParallel LaneLimits
	Originals        OriginalsPolicy
}

// TranscodeOrder is the order a library's files are worked in.
type TranscodeOrder string

// The orders; found is the order the analysis recorded the files in.
const (
	OrderFound        TranscodeOrder = "found"
	OrderAlphabetical TranscodeOrder = "alphabetical"
	OrderLargest      TranscodeOrder = "largest"
	OrderSmallest     TranscodeOrder = "smallest"
	OrderOldest       TranscodeOrder = "oldest"
	OrderNewest       TranscodeOrder = "newest"
)

// TranscodeOrders lists every order.
var TranscodeOrders = []TranscodeOrder{OrderFound, OrderAlphabetical, OrderLargest, OrderSmallest, OrderOldest, OrderNewest}

// CategoryTranscode is one library's transcode row; a library without one is off with priority 0.
type CategoryTranscode struct {
	CategoryID int64
	Enabled    bool
	Priority   int64
	Order      TranscodeOrder
}

// TranscodeGlobal answers the instance-wide policy.
func (s *Snapshot) TranscodeGlobal() TranscodeGlobal {
	lanes := func(cpu, gpu, audio string, defaults LaneLimits) LaneLimits {
		return LaneLimits{CPU: s.policyInt(cpu, defaults.CPU), GPU: s.policyInt(gpu, defaults.GPU), Audio: s.policyInt(audio, defaults.Audio)}
	}
	return TranscodeGlobal{
		Enabled:          s.policyBool(KeyTranscodeEnabled, false),
		Parallel:         lanes(KeyTranscodeParallelCPU, KeyTranscodeParallelGPU, KeyTranscodeParallelAudio, LaneLimits{CPU: 1, GPU: 2, Audio: 4}),
		PlaybackParallel: lanes(KeyTranscodePlaybackParallelCPU, KeyTranscodePlaybackParallelGPU, KeyTranscodePlaybackParallelAudio, LaneLimits{GPU: 1, Audio: 1}),
		Originals: OriginalsPolicy{
			RetainDays: s.policyInt(KeyTranscodeOriginalsRetainDays, 7), CapGB: s.policyInt(KeyTranscodeOriginalsCapGB, 100),
		},
	}
}

// TranscodeEnabled is the master switch.
func (s *Snapshot) TranscodeEnabled() bool { return s.TranscodeGlobal().Enabled }

// SetTranscodeGlobal validates and writes the whole instance-wide policy.
func (c *Cache) SetTranscodeGlobal(ctx context.Context, cl clock.Clock, global TranscodeGlobal) error {
	var problems config.Problems
	for path, value := range map[string]int64{
		"parallel.cpu": global.Parallel.CPU, "parallel.gpu": global.Parallel.GPU, "parallel.audio": global.Parallel.Audio,
		"playback_parallel.cpu": global.PlaybackParallel.CPU, "playback_parallel.gpu": global.PlaybackParallel.GPU, "playback_parallel.audio": global.PlaybackParallel.Audio,
	} {
		if value < 0 || value > maxTranscodeParallel {
			problems = append(problems, config.Problem{Path: path, Value: strconv.FormatInt(value, 10), Expectation: "0 to 16"})
		}
	}
	if global.Originals.RetainDays < 0 || global.Originals.RetainDays > 3650 {
		problems = append(problems, config.Problem{Path: "originals.retain_days", Value: strconv.FormatInt(global.Originals.RetainDays, 10), Expectation: "0 to 3650"})
	}
	if global.Originals.CapGB < 0 {
		problems = append(problems, config.Problem{Path: "originals.cap_gb", Value: strconv.FormatInt(global.Originals.CapGB, 10), Expectation: "0 or more"})
	}
	if len(problems) > 0 {
		return problems
	}
	number := func(value int64) string { return strconv.FormatInt(value, 10) }
	return c.setPolicies(ctx, cl, map[string]string{
		KeyTranscodeEnabled:               boolPolicy(global.Enabled),
		KeyTranscodeParallelCPU:           number(global.Parallel.CPU),
		KeyTranscodeParallelGPU:           number(global.Parallel.GPU),
		KeyTranscodeParallelAudio:         number(global.Parallel.Audio),
		KeyTranscodePlaybackParallelCPU:   number(global.PlaybackParallel.CPU),
		KeyTranscodePlaybackParallelGPU:   number(global.PlaybackParallel.GPU),
		KeyTranscodePlaybackParallelAudio: number(global.PlaybackParallel.Audio),
		KeyTranscodeOriginalsRetainDays:   number(global.Originals.RetainDays),
		KeyTranscodeOriginalsCapGB:        number(global.Originals.CapGB),
	})
}

// StoredTranscodeSetup is a target's setup with the revision it was saved at.
type StoredTranscodeSetup struct {
	Setup    TranscodeSetup
	Revision int64
	// Stored is false for the generated default of a target nobody saved a setup for; Revision is then 0.
	Stored bool
}

// TranscodeSetup answers a target's setup: the saved document over the default, or the default; ErrNotFound for a target that does not exist.
func (c *Cache) TranscodeSetup(ctx context.Context, targetID int64) (StoredTranscodeSetup, error) {
	target, found := c.Snapshot().QualityTarget(targetID)
	if !found {
		return StoredTranscodeSetup{}, fmt.Errorf("%w: quality target %d", ErrNotFound, targetID)
	}
	row, err := c.queries.GetTranscodeSetup(ctx, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredTranscodeSetup{Setup: DefaultTranscodeSetup(target)}, nil
	}
	if err != nil {
		return StoredTranscodeSetup{}, fmt.Errorf("settings: transcode setup: %w", err)
	}
	setup, err := ParseTranscodeSetup([]byte(row.SetupJson), target)
	if err != nil {
		return StoredTranscodeSetup{}, err
	}
	return StoredTranscodeSetup{Setup: setup, Revision: row.Revision, Stored: true}, nil
}

// SetTranscodeSetup validates and saves a target's setup; the revision goes up by one.
func (c *Cache) SetTranscodeSetup(ctx context.Context, cl clock.Clock, targetID int64, setup TranscodeSetup) error {
	target, found := c.Snapshot().QualityTarget(targetID)
	if !found {
		return fmt.Errorf("%w: quality target %d", ErrNotFound, targetID)
	}
	if problems := setup.Validate(target); len(problems) > 0 {
		return problems
	}
	raw, err := json.Marshal(setup)
	if err != nil {
		return fmt.Errorf("settings: transcode setup: %w", err)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpsertTranscodeSetup(ctx, settingsdb.UpsertTranscodeSetupParams{TargetID: targetID, SetupJson: string(raw), UpdatedAt: cl.Now().Unix()})
	})
}

// ResetTranscodeSetup writes the target's generated default over its setup; the revision still goes up, never back.
func (c *Cache) ResetTranscodeSetup(ctx context.Context, cl clock.Clock, targetID int64) error {
	target, found := c.Snapshot().QualityTarget(targetID)
	if !found {
		return fmt.Errorf("%w: quality target %d", ErrNotFound, targetID)
	}
	return c.SetTranscodeSetup(ctx, cl, targetID, DefaultTranscodeSetup(target))
}

// CategoryTranscodes answers every library's transcode row, a library without one off with priority 0.
func (c *Cache) CategoryTranscodes(ctx context.Context) ([]CategoryTranscode, error) {
	rows, err := c.queries.ListCategoryTranscode(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: transcode libraries: %w", err)
	}
	saved := map[int64]settingsdb.CategoryTranscode{}
	for _, row := range rows {
		saved[row.CategoryID] = row
	}
	var out []CategoryTranscode
	for _, category := range c.Snapshot().Categories() {
		one := CategoryTranscode{CategoryID: category.ID, Order: OrderFound}
		if row, found := saved[category.ID]; found {
			one.Enabled, one.Priority, one.Order = row.Enabled == 1, row.Priority, TranscodeOrder(row.ListOrder)
		}
		out = append(out, one)
	}
	return out, nil
}

// SetCategoryTranscode validates and saves one library's row.
func (c *Cache) SetCategoryTranscode(ctx context.Context, row CategoryTranscode) error {
	if _, found := c.Snapshot().Category(row.CategoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, row.CategoryID)
	}
	var problems config.Problems
	if row.Priority < -100 || row.Priority > 100 {
		problems = append(problems, config.Problem{Path: "priority", Value: strconv.FormatInt(row.Priority, 10), Expectation: "-100 to 100"})
	}
	if !slices.Contains(TranscodeOrders, row.Order) {
		problems = append(problems, config.Problem{Path: "order", Value: string(row.Order), Expectation: "one of found, alphabetical, largest, smallest, oldest, newest"})
	}
	if len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpsertCategoryTranscode(ctx, settingsdb.UpsertCategoryTranscodeParams{
			CategoryID: row.CategoryID, Enabled: boolToInt(row.Enabled), Priority: row.Priority, ListOrder: string(row.Order),
		})
	})
}

// ListTranscodeSetupRevisions answers the revision of every saved setup by target, for what must notice a setup changed.
func (c *Cache) ListTranscodeSetupRevisions(ctx context.Context) (map[int64]int64, error) {
	rows, err := c.queries.ListTranscodeSetups(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: transcode setups: %w", err)
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.TargetID] = row.Revision
	}
	return out, nil
}

// TranscodeLibrary is a library's transcode row with what the interface shows beside it; Target is nil for a library with none.
type TranscodeLibrary struct {
	CategoryTranscode
	Name      string
	MediaType string
	Target    *QualityTarget
}

// Supported says the library has an audio or video target, the only kinds that are converted.
func (l TranscodeLibrary) Supported() bool {
	return l.Target != nil && l.Target.TranscodeKind() != "other"
}

// TranscodeLibraries answers every library with its row and target.
func (c *Cache) TranscodeLibraries(ctx context.Context) ([]TranscodeLibrary, error) {
	rows, err := c.CategoryTranscodes(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := c.Snapshot()
	var out []TranscodeLibrary
	for _, row := range rows {
		if category, found := snapshot.Category(row.CategoryID); found {
			out = append(out, TranscodeLibrary{CategoryTranscode: row, Name: category.Name, MediaType: category.MediaType, Target: category.QualityTarget})
		}
	}
	return out, nil
}
