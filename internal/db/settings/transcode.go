package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The instance-wide transcode policy keys (documentation/transcoding.md).
const (
	KeyTranscodeEnabled                     = "transcode.enabled"
	KeyTranscodeParallelAuto                = "transcode.parallel_auto"
	KeyTranscodePlaybackParallelAuto        = "transcode.playback_parallel_auto"
	KeyTranscodeParallelCPU                 = "transcode.parallel_cpu"
	KeyTranscodeParallelGPU                 = "transcode.parallel_gpu"
	KeyTranscodeParallelAudio               = "transcode.parallel_audio"
	KeyTranscodeParallelBooks               = "transcode.parallel_books"
	KeyTranscodePlaybackParallelCPU         = "transcode.playback_parallel_cpu"
	KeyTranscodePlaybackParallelGPU         = "transcode.playback_parallel_gpu"
	KeyTranscodePlaybackParallelAudio       = "transcode.playback_parallel_audio"
	KeyTranscodePlaybackParallelBooks       = "transcode.playback_parallel_books"
	KeyTranscodeOriginalsRetainDays         = "transcode.originals_retain_days"
	KeyTranscodeOriginalsCapGB              = "transcode.originals_cap_gb"
	maxTranscodeParallel              int64 = 16
)

// LaneLimits are how many conversions run at once on each kind of work. The limits are ceilings down the order CPU, GPU,
// video, video hardware, audio, books: a conversion counts against its own lane's ceiling and every later one, so each limit is at
// least the one before it (CPU 1, GPU 2, audio 4 allows one CPU and one GPU job beside two audio ones).
type LaneLimits struct{ CPU, GPU, Audio, Books int64 }

// InOrder lists the limits in ceiling order with the settings path of each, for validation.
func (l LaneLimits) InOrder(prefix string) []NamedLimit {
	return []NamedLimit{{prefix + ".cpu", l.CPU}, {prefix + ".gpu", l.GPU}, {prefix + ".audio", l.Audio}, {prefix + ".books", l.Books}}
}

// NamedLimit is one lane's limit with its settings path.
type NamedLimit struct {
	Path  string
	Value int64
}

// OriginalsPolicy is how long a replaced original stays in the recycle bin (0 days: the bin's own rule) and the most
// space they may take together (0 GB: no cap).
type OriginalsPolicy struct{ RetainDays, CapGB int64 }

// TranscodeGlobal is the instance-wide transcode policy.
type TranscodeGlobal struct {
	Enabled          bool
	Parallel         LaneLimits
	PlaybackParallel LaneLimits
	// ParallelAuto and PlaybackParallelAuto say the row follows the hardware until limits are saved; see Resolved.
	ParallelAuto, PlaybackParallelAuto bool
	Originals                          OriginalsPolicy
}

// LaneHardware is what the automatic lane limits follow: the cores the container may use and the hardware encode engines
// (zero: no hardware encoder works).
type LaneHardware struct{ Cores, EncodeEngines int }

// AutoLaneLimits are the limits while nothing plays and while the web player converts; the rule is in documentation/transcoding.md.
func AutoLaneLimits(hardware LaneHardware) (normal, playing LaneLimits) {
	cores := max(hardware.Cores, 1)
	engines := max(hardware.EncodeEngines, 0)
	normal.CPU = 1
	normal.GPU = max(int64(engines), normal.CPU)
	normal.Audio = max(int64(cores/2), 1, normal.GPU)
	normal.Books = normal.Audio
	if engines > 0 {
		playing.GPU = int64(engines - 1)
	}
	playing.Audio = max(int64(cores/4), 1, playing.GPU)
	playing.Books = playing.Audio
	clamp := func(limits LaneLimits) LaneLimits {
		return LaneLimits{CPU: min(limits.CPU, maxTranscodeParallel), GPU: min(limits.GPU, maxTranscodeParallel), Audio: min(limits.Audio, maxTranscodeParallel), Books: min(limits.Books, maxTranscodeParallel)}
	}
	return clamp(normal), clamp(playing)
}

// Resolved is the policy with the rows that follow the hardware replaced by what AutoLaneLimits gives for it.
func (g TranscodeGlobal) Resolved(hardware LaneHardware) TranscodeGlobal {
	normal, playing := AutoLaneLimits(hardware)
	if g.ParallelAuto {
		g.Parallel = normal
	}
	if g.PlaybackParallelAuto {
		g.PlaybackParallel = playing
	}
	return g
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
	lanes := func(cpu, gpu, audio, books string, defaults LaneLimits) LaneLimits {
		return LaneLimits{
			CPU: s.policyInt(cpu, defaults.CPU), GPU: s.policyInt(gpu, defaults.GPU), Audio: s.policyInt(audio, defaults.Audio),
			Books: s.policyInt(books, defaults.Books),
		}
	}
	return TranscodeGlobal{
		Enabled:              s.policyBool(KeyTranscodeEnabled, false),
		Parallel:             lanes(KeyTranscodeParallelCPU, KeyTranscodeParallelGPU, KeyTranscodeParallelAudio, KeyTranscodeParallelBooks, LaneLimits{CPU: 1, GPU: 2, Audio: 4, Books: 4}),
		PlaybackParallel:     lanes(KeyTranscodePlaybackParallelCPU, KeyTranscodePlaybackParallelGPU, KeyTranscodePlaybackParallelAudio, KeyTranscodePlaybackParallelBooks, LaneLimits{GPU: 1, Audio: 1, Books: 1}),
		ParallelAuto:         s.policyBool(KeyTranscodeParallelAuto, true),
		PlaybackParallelAuto: s.policyBool(KeyTranscodePlaybackParallelAuto, true),
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
	rows := [][]NamedLimit{}
	if !global.ParallelAuto {
		rows = append(rows, global.Parallel.InOrder("parallel"))
	}
	if !global.PlaybackParallelAuto {
		rows = append(rows, global.PlaybackParallel.InOrder("playback_parallel"))
	}
	for _, row := range rows {
		for index, limit := range row {
			if limit.Value < 0 || limit.Value > maxTranscodeParallel {
				problems = append(problems, config.Problem{Path: limit.Path, Value: strconv.FormatInt(limit.Value, 10), Expectation: "0 to 16"})
			}
			if index > 0 && limit.Value < row[index-1].Value {
				problems = append(problems, config.Problem{Path: limit.Path, Value: strconv.FormatInt(limit.Value, 10), Expectation: "at least " + row[index-1].Path[strings.LastIndex(row[index-1].Path, ".")+1:] + "'s: each limit counts the lanes before it"})
			}
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
	values := map[string]string{
		KeyTranscodeEnabled:              boolPolicy(global.Enabled),
		KeyTranscodeParallelAuto:         boolPolicy(global.ParallelAuto),
		KeyTranscodePlaybackParallelAuto: boolPolicy(global.PlaybackParallelAuto),
		KeyTranscodeOriginalsRetainDays:  number(global.Originals.RetainDays),
		KeyTranscodeOriginalsCapGB:       number(global.Originals.CapGB),
	}
	if !global.ParallelAuto {
		values[KeyTranscodeParallelCPU], values[KeyTranscodeParallelGPU] = number(global.Parallel.CPU), number(global.Parallel.GPU)
		values[KeyTranscodeParallelAudio], values[KeyTranscodeParallelBooks] = number(global.Parallel.Audio), number(global.Parallel.Books)
	}
	if !global.PlaybackParallelAuto {
		values[KeyTranscodePlaybackParallelCPU], values[KeyTranscodePlaybackParallelGPU] = number(global.PlaybackParallel.CPU), number(global.PlaybackParallel.GPU)
		values[KeyTranscodePlaybackParallelAudio], values[KeyTranscodePlaybackParallelBooks] = number(global.PlaybackParallel.Audio), number(global.PlaybackParallel.Books)
	}
	return c.setPolicies(ctx, cl, values)
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

// Kind is what the library's files are converted as: video, audio, comic for a comics library, or other.
func (l TranscodeLibrary) Kind() string {
	switch {
	case l.Target == nil:
		return "other"
	case l.MediaType == "comic":
		return "comic"
	}
	return l.Target.TranscodeKind()
}

// Supported says the library has an audio, video or comics target, the only kinds that are converted.
func (l TranscodeLibrary) Supported() bool { return l.Kind() != "other" }

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
