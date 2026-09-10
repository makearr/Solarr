package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// baseEpoch is fixed, so two runs of the same spec produce identical rows.
const baseEpoch int64 = 1_700_000_000

// rootFolderID and the profile and category ids below live in settings.db, which a
// fixture does not have: the values only have to be stable (ADR-25).
const (
	rootFolderID = 1
	categoryID   = 1
	profileID    = 1
)

// mediaKind is one media type and how many files a work of it holds.
type mediaKind struct {
	name  string
	parts int
	// set is true where a work is satisfied by many files (ADR-23 set coverage).
	set bool
}

// mixPattern is the user's library at roughly 50% music, 35% TV, 15% film, cycled
// rather than sampled so the proportions hold exactly at any scale.
var mixPattern = buildMixPattern()

func buildMixPattern() []mediaKind {
	music := mediaKind{name: "music", parts: 12, set: true}
	television := mediaKind{name: "tv", parts: 10, set: true}
	film := mediaKind{name: "movie", parts: 1}

	pattern := make([]mediaKind, 0, 20)
	for range 10 {
		pattern = append(pattern, music)
	}
	for range 7 {
		pattern = append(pattern, television)
	}
	for range 3 {
		pattern = append(pattern, film)
	}
	return pattern
}

var titleHeads = []string{
	"Hollow", "Silver", "Broken", "Distant", "Northern", "Quiet", "Amber", "Crooked",
	"Endless", "Golden", "Iron", "Lonely", "Marble", "Restless", "Sudden", "Winter",
}

var titleTails = []string{
	"Rivers", "Machines", "Harbour", "Signals", "Gardens", "Letters", "Mountains", "Voices",
	"Windows", "Bridges", "Orchards", "Lanterns", "Stations", "Mirrors", "Weather", "Streets",
}

// repeatedTitle is shared by every eighth work, so a page boundary lands inside a
// group of identical sort titles and the cursor tie-break is measured, not assumed.
const repeatedTitle = "Common Ground"

// repeatEvery is small enough that the group outgrows a page at any corpus size.
const repeatEvery = 8

// workPlan is one work and everything hanging off it, decided before any row is
// written so each phase can batch its own row type.
type workPlan struct {
	kind      mediaKind
	title     string
	sortTitle string
	year      int64
	id        int64
	desiredID int64
}

// Populate fills an already migrated library database. Exported so a caller can supply
// its own store: the transaction budget is only assertable from the caller's config.
func Populate(ctx context.Context, opened *store.Store, spec Spec) error {
	spec = spec.withDefaults()
	random := rand.New(rand.NewPCG(uint64(spec.Seed), uint64(spec.Seed)))
	plans := planWorks(random, spec)

	attributes, err := insertAttributes(ctx, opened)
	if err != nil {
		return err
	}
	if err := insertWorks(ctx, opened, plans); err != nil {
		return err
	}
	if err := insertWorkDetail(ctx, opened, plans); err != nil {
		return err
	}
	if err := insertFiles(ctx, opened, plans, attributes); err != nil {
		return err
	}
	return insertCounters(ctx, opened, plans, spec)
}

func planWorks(random *rand.Rand, spec Spec) []*workPlan {
	plans := make([]*workPlan, 0, spec.Files/8)
	for files, index := 0, 0; files < spec.Files; index++ {
		kind := mixPattern[index%len(mixPattern)]
		if remaining := spec.Files - files; kind.parts > remaining {
			kind.parts = remaining
		}
		title := planTitle(random, index)
		plans = append(plans, &workPlan{
			kind:      kind,
			title:     title,
			sortTitle: strings.ToLower(title),
			year:      int64(1970 + random.IntN(56)),
		})
		files += kind.parts
	}
	return plans
}

func planTitle(random *rand.Rand, index int) string {
	head := titleHeads[random.IntN(len(titleHeads))]
	tail := titleTails[random.IntN(len(titleTails))]
	suffixed := random.IntN(2) == 0
	number := random.IntN(90) + 10

	if index%repeatEvery == 0 {
		return repeatedTitle
	}
	if suffixed {
		return head + " " + tail + " " + strconv.Itoa(number)
	}
	return head + " " + tail
}

// insertAttributes interns the categorical sets every file then points at: a few dozen
// combinations across 500k files is the premise interning rests on (section 5.2).
func insertAttributes(ctx context.Context, opened *store.Store) ([]int64, error) {
	type combination struct {
		container string
		codec     string
		lossless  int64
		sample    int64
		bitDepth  int64
	}

	combinations := []combination{
		{"flac", "flac", 1, 44100, 16},
		{"flac", "flac", 1, 96000, 24},
		{"mp3", "mp3", 0, 44100, 16},
		{"m4a", "aac", 0, 44100, 16},
		{"mkv", "h264", 0, 48000, 8},
		{"mkv", "h265", 0, 48000, 10},
		{"mp4", "h264", 0, 48000, 8},
		{"mkv", "av1", 0, 48000, 10},
	}

	interned := make([]int64, 0, len(combinations))
	err := opened.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(opened.Read()).WithTx(tx)
		for index, item := range combinations {
			row, err := queries.InternAttributes(ctx, librarydb.InternAttributesParams{
				AttrHash:   fmt.Sprintf("fixture-%02d", index),
				Container:  text(item.container),
				AudioCodec: text(item.codec),
				Lossless:   number(item.lossless),
				SampleRate: number(item.sample),
				BitDepth:   number(item.bitDepth),
				Languages:  text(`["en"]`),
			})
			if err != nil {
				return err
			}
			interned = append(interned, row.ID)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fixture: intern attributes: %w", err)
	}
	return interned, nil
}

func insertWorks(ctx context.Context, opened *store.Store, plans []*workPlan) error {
	err := eachChunk(ctx, opened, 1, plans, func(ctx context.Context, queries *librarydb.Queries, plan *workPlan) error {
		row, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType:        plan.kind.name,
			CanonicalTitle:   plan.title,
			SortTitle:        plan.sortTitle,
			OriginalLanguage: text("en"),
			Year:             number(plan.year),
			RuntimeMs:        number(int64(plan.kind.parts) * 240_000),
			AddedAt:          baseEpoch,
			UpdatedAt:        baseEpoch,
		})
		if err != nil {
			return err
		}
		plan.id = row.ID
		return nil
	})
	if err != nil {
		return fmt.Errorf("fixture: works: %w", err)
	}
	return nil
}

// insertWorkDetail writes the title, provider id, edition, parts and desired item that
// every list and lookup benchmark joins against.
func insertWorkDetail(ctx context.Context, opened *store.Store, plans []*workPlan) error {
	const rowsPerWork = 16

	err := eachChunk(ctx, opened, rowsPerWork, plans, func(ctx context.Context, queries *librarydb.Queries, plan *workPlan) error {
		if _, err := queries.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: plan.id, Title: plan.title, Normalized: plan.sortTitle,
			Language: text("en"), Kind: "canonical", Source: text("fixture"),
		}); err != nil {
			return err
		}
		if err := queries.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "musicbrainz", ExternalID: fmt.Sprintf("rg-%d", plan.id),
			WorkID: plan.id, Confidence: 1, FirstSeenAt: baseEpoch,
		}); err != nil {
			return err
		}
		if _, err := queries.CreateEdition(ctx, librarydb.CreateEditionParams{
			WorkID: plan.id, Kind: "standard", Label: "Standard",
			Year: number(plan.year), IsDefault: 1,
		}); err != nil {
			return err
		}

		for part := range partCount(plan) {
			if _, err := queries.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
				WorkID:    plan.id,
				MediaRef:  mediaRef(plan, part),
				SortKey:   fmt.Sprintf("%05d", part),
				Title:     text(fmt.Sprintf("%s, part %d", plan.title, part+1)),
				RuntimeMs: number(240_000),
				Monitored: 1,
			}); err != nil {
				return err
			}
		}

		desired, err := queries.CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: plan.id, CategoryID: categoryID, ProfileID: profileID, Monitored: 1,
			SatisfactionKind: satisfaction(plan),
			RequiredCount:    number(int64(plan.kind.parts)),
			PresentCount:     int64(plan.kind.parts),
			State:            "COMPLETE",
			Generation:       1,
		})
		if err != nil {
			return err
		}
		plan.desiredID = desired.ID

		// Bounded sort-name modulus, no real person row needed (ADR-28 plan
		// benchmark only orders by this column, migration 0021).
		return queries.SetWorkPrimaryArtist(ctx, librarydb.SetWorkPrimaryArtistParams{
			ID: plan.id, PrimaryArtistSortName: text(fmt.Sprintf("Artist %04d", plan.id%500)),
		})
	})
	if err != nil {
		return fmt.Errorf("fixture: work detail: %w", err)
	}
	return nil
}

// insertFiles writes the 500k-row half of the corpus: the bytes, the path pointing at
// them, and the link that makes the desired item complete.
func insertFiles(ctx context.Context, opened *store.Store, plans []*workPlan, attributes []int64) error {
	const rowsPerFile = 3
	maximumParts := 0
	for _, plan := range plans {
		maximumParts = max(maximumParts, plan.kind.parts)
	}

	err := eachChunk(ctx, opened, rowsPerFile*maximumParts, plans, func(ctx context.Context, queries *librarydb.Queries, plan *workPlan) error {
		for part := range plan.kind.parts {
			serial := plan.id*1000 + int64(part)
			attribute := attributes[serial%int64(len(attributes))]

			// A tenth stay unfingerprinted, so the resumable ADR-35 cursor has work
			// to find at every scale.
			fingerprinted := sql.NullInt64{Int64: baseEpoch, Valid: serial%10 != 0}

			objectID, err := queries.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
				SizeBytes:            3_000_000 + serial%40_000_000,
				QuickHash:            fmt.Sprintf("q%012x", serial),
				Oshash:               text(fmt.Sprintf("os%012x", serial)),
				AudioFpPrefix:        number(serial % 65536),
				FingerprintedAt:      fingerprinted,
				ClaimedAttributesID:  number(attribute),
				MeasuredAttributesID: number(attribute),
				DurationMs:           number(120_000 + serial%480_000),
				OverallBitrate:       number(128_000 + serial%1_000_000),
				FirstSeenAt:          baseEpoch,
				VerifiedAt:           number(baseEpoch),
			})
			if err != nil {
				return err
			}
			if _, err := queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
				MediaObjectID: objectID, RootFolderID: rootFolderID,
				RelPath:   relativePath(plan, part),
				LinkKind:  "hardlink",
				IsPrimary: 1, CreatedAt: baseEpoch,
			}); err != nil {
				return err
			}
			if err := queries.AttachFile(ctx, librarydb.AttachFileParams{
				DesiredItemID: plan.desiredID, MediaObjectID: objectID,
				Role: "primary", MediaRef: mediaRef(plan, part), AddedAt: baseEpoch,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fixture: files: %w", err)
	}
	return nil
}

// insertCounters fills the table every dashboard figure is read from, since ADR-28
// forbids answering one with a live aggregate.
func insertCounters(ctx context.Context, opened *store.Store, plans []*workPlan, spec Spec) error {
	values := map[string]int64{
		"works":     int64(len(plans)),
		"files":     int64(spec.Files),
		"monitored": int64(len(plans)),
		"missing":   0,
	}

	err := opened.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(opened.Read()).WithTx(tx)
		for metric, value := range values {
			if err := queries.SetCounter(ctx, librarydb.SetCounterParams{
				Scope: "library", Metric: metric, Value: value, UpdatedAt: baseEpoch,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("fixture: counters: %w", err)
	}
	return nil
}

// eachChunk submits items in transactions bounded by Invariant 2's row budget.
func eachChunk[T any](
	ctx context.Context,
	opened *store.Store,
	rowsPerItem int,
	items []T,
	run func(context.Context, *librarydb.Queries, T) error,
) error {
	size := max(1, rowsPerTransaction/rowsPerItem)
	return store.WriteBatched(ctx, opened, store.PrioBulk, size, items,
		func(ctx context.Context, tx *sql.Tx, chunk []T) error {
			queries := librarydb.New(opened.Read()).WithTx(tx)
			for _, item := range chunk {
				if err := run(ctx, queries, item); err != nil {
					return err
				}
			}
			return nil
		})
}

// partCount is zero for a work one file satisfies: a film has no parts to address.
func partCount(plan *workPlan) int {
	if !plan.kind.set {
		return 0
	}
	return plan.kind.parts
}

func satisfaction(plan *workPlan) string {
	if plan.kind.set {
		return "SET"
	}
	return "SINGLE"
}

// mediaRef is the module's vocabulary, opaque to core (ADR-23).
func mediaRef(plan *workPlan, part int) string {
	switch {
	case !plan.kind.set:
		return ""
	case plan.kind.name == "tv":
		return fmt.Sprintf("s%02de%02d", part/10+1, part%10+1)
	default:
		return fmt.Sprintf("disc1:track%02d", part+1)
	}
}

// relativePath carries the work id because titles repeat and the path is unique per root.
func relativePath(plan *workPlan, part int) string {
	name := mediaRef(plan, part)
	if name == "" {
		name = "feature"
	}
	return fmt.Sprintf("%s/%d %s/%s.%s", plan.kind.name, plan.id, plan.sortTitle, name, extension(plan))
}

func extension(plan *workPlan) string {
	if plan.kind.name == "music" {
		return "flac"
	}
	return "mkv"
}

func text(value string) sql.NullString { return sql.NullString{String: value, Valid: true} }

func number(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
