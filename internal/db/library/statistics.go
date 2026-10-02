package library

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// The metrics the rollup writes into stat_rollup (period 0, the current snapshot). A row's dimension is
// "<category id>|<key>"; value and count are what each says below.
const (
	// StatFilesByCodec: value bytes, count files, key the audio codec.
	StatFilesByCodec = "files_by_codec"
	// StatAlbumsByState: value and count the monitored albums, key their desired state.
	StatAlbumsByState = "albums_by_state"
	// StatDownloadsByState: value and count the downloads of the last 30 days, key their state.
	StatDownloadsByState = "downloads_by_state"
	// StatGrowthByMonth: value bytes, count files first seen in the month, key "YYYY-MM".
	StatGrowthByMonth = "growth_by_month"
	// StatDecisionsByOutcome: value and count the release decisions of the last 30 days, key ACCEPT, REJECT or DEFER.
	StatDecisionsByOutcome = "decisions_by_outcome"
)

// StatisticsWindowDays is how far back the download figures reach.
const StatisticsWindowDays = 30

// Statistics is section 21.12's rollup: a background job computes it, the statistics page only reads it.
type Statistics struct{ store *store.Store }

// NewStatistics builds it over an open library store.
func NewStatistics(opened *store.Store) Statistics { return Statistics{store: opened} }

// StatFigure is one rollup row, for one library.
type StatFigure struct {
	Key        string
	Value      float64
	Count      int64
	ComputedAt int64
}

// Compute rebuilds every metric. categoryOfRoot says which library each root folder belongs to (settings.db owns
// that, so the caller composes it); a root in none is left out.
func (s Statistics) Compute(ctx context.Context, categoryOfRoot map[int64]int64, now int64) error {
	reader := librarydb.New(s.store.Read())
	codecs, err := reader.AggregateFilesByRootAndCodec(ctx)
	if err != nil {
		return fmt.Errorf("library: statistics: files: %w", err)
	}
	albums, err := reader.AggregateAlbumsByCategoryAndState(ctx)
	if err != nil {
		return fmt.Errorf("library: statistics: albums: %w", err)
	}
	downloads, err := reader.AggregateAcquisitionsByCategoryAndState(ctx, now-StatisticsWindowDays*86_400)
	if err != nil {
		return fmt.Errorf("library: statistics: downloads: %w", err)
	}

	added, err := reader.AggregateFilesAddedByRootAndMonth(ctx)
	if err != nil {
		return fmt.Errorf("library: statistics: growth: %w", err)
	}
	decisions, err := reader.AggregateDecisionsByCategoryAndOutcome(ctx, now-StatisticsWindowDays*86_400)
	if err != nil {
		return fmt.Errorf("library: statistics: decisions: %w", err)
	}

	type figure struct {
		metric, dimension string
		value             float64
		count             int64
	}
	var figures []figure
	byCodec := map[string]*figure{}
	for _, row := range codecs {
		category, found := categoryOfRoot[row.RootFolderID]
		if !found {
			continue
		}
		dimension := dimensionOf(category, row.Codec)
		if existing, ok := byCodec[dimension]; ok {
			existing.value += float64(row.Bytes)
			existing.count += row.Files
			continue
		}
		byCodec[dimension] = &figure{StatFilesByCodec, dimension, float64(row.Bytes), row.Files}
	}
	for _, one := range byCodec {
		figures = append(figures, *one)
	}
	byMonth := map[string]*figure{}
	for _, row := range added {
		category, found := categoryOfRoot[row.RootFolderID]
		if !found {
			continue
		}
		dimension := dimensionOf(category, row.Month)
		if existing, ok := byMonth[dimension]; ok {
			existing.value += float64(row.Bytes)
			existing.count += row.Files
			continue
		}
		byMonth[dimension] = &figure{StatGrowthByMonth, dimension, float64(row.Bytes), row.Files}
	}
	for _, one := range byMonth {
		figures = append(figures, *one)
	}
	for _, row := range decisions {
		figures = append(figures, figure{StatDecisionsByOutcome, dimensionOf(row.CategoryID, row.Outcome), float64(row.Decisions), row.Decisions})
	}
	for _, row := range albums {
		figures = append(figures, figure{StatAlbumsByState, dimensionOf(row.CategoryID, row.State), float64(row.Albums), row.Albums})
	}
	for _, row := range downloads {
		figures = append(figures, figure{StatDownloadsByState, dimensionOf(row.CategoryID, row.State), float64(row.Downloads), row.Downloads})
	}

	return s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, metric := range []string{StatFilesByCodec, StatAlbumsByState, StatDownloadsByState, StatGrowthByMonth, StatDecisionsByOutcome} {
			if err := queries.DeleteStatRollupMetric(ctx, metric); err != nil {
				return err
			}
		}
		for _, one := range figures {
			if err := queries.SetStatRollup(ctx, librarydb.SetStatRollupParams{
				Metric: one.metric, Dimension: one.dimension, Period: 0, Value: one.value,
				Count: sql.NullInt64{Int64: one.count, Valid: true}, ComputedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func dimensionOf(categoryID int64, key string) string {
	return strconv.FormatInt(categoryID, 10) + "|" + key
}

// ForCategory answers a metric's current figures for one library, largest first; empty before the first rollup.
func (s Statistics) ForCategory(ctx context.Context, metric string, categoryID int64) ([]StatFigure, error) {
	rows, err := librarydb.New(s.store.Read()).ListStatRollup(ctx, librarydb.ListStatRollupParams{
		Metric: metric, Since: 0, PageSize: 10_000,
	})
	if err != nil {
		return nil, fmt.Errorf("library: statistics: %s: %w", metric, err)
	}
	prefix := strconv.FormatInt(categoryID, 10) + "|"
	var figures []StatFigure
	for _, row := range rows {
		if row.Period != 0 || !strings.HasPrefix(row.Dimension, prefix) {
			continue
		}
		figures = append(figures, StatFigure{
			Key: strings.TrimPrefix(row.Dimension, prefix), Value: row.Value, Count: row.Count.Int64, ComputedAt: row.ComputedAt,
		})
	}
	return figures, nil
}

// StatDiscographyCoverage is the artists' coverage metric: dimension "<category id>|<person id>", value the albums
// held, count the albums their discography has under the default filters.
const StatDiscographyCoverage = "discography_coverage"

// ArtistHeld is an artist with something held in a library: their MusicBrainz id and the release groups held.
type ArtistHeld struct {
	PersonID      int64
	Name          string
	ArtistMBID    string
	ReleaseGroups []string
}

// ArtistsHeld lists the artists with an album held in categoryID, with the release groups held, by person id.
func (s Statistics) ArtistsHeld(ctx context.Context, categoryID int64) ([]ArtistHeld, error) {
	rows, err := librarydb.New(s.store.Read()).ListHeldReleaseGroupsByArtist(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: artists held: %w", err)
	}
	var artists []ArtistHeld
	for _, row := range rows {
		if len(artists) == 0 || artists[len(artists)-1].PersonID != row.PersonID {
			artists = append(artists, ArtistHeld{PersonID: row.PersonID, Name: row.Name, ArtistMBID: row.ArtistMbid})
		}
		if row.ReleaseGroupMbid != "" {
			last := &artists[len(artists)-1]
			last.ReleaseGroups = append(last.ReleaseGroups, row.ReleaseGroupMbid)
		}
	}
	return artists, nil
}

// DiscographyCoverage is one artist's held albums against their discography.
type DiscographyCoverage struct {
	PersonID    int64
	Held, Total int64
}

// SetDiscographyCoverage replaces a library's coverage rows.
func (s Statistics) SetDiscographyCoverage(ctx context.Context, categoryID int64, rows []DiscographyCoverage, now int64) error {
	return s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.DeleteStatRollupMetricForCategory(ctx, librarydb.DeleteStatRollupMetricForCategoryParams{
			Metric: StatDiscographyCoverage, Prefix: strconv.FormatInt(categoryID, 10) + "|",
		}); err != nil {
			return err
		}
		for _, row := range rows {
			if err := queries.SetStatRollup(ctx, librarydb.SetStatRollupParams{
				Metric: StatDiscographyCoverage, Dimension: dimensionOf(categoryID, strconv.FormatInt(row.PersonID, 10)),
				Period: 0, Value: float64(row.Held), Count: sql.NullInt64{Int64: row.Total, Valid: true}, ComputedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// IncompleteDiscographies lists a library's artists whose discography has albums not held, most missing first.
func (s Statistics) IncompleteDiscographies(ctx context.Context, categoryID int64, limit int) ([]DiscographyCoverage, error) {
	figures, err := s.ForCategory(ctx, StatDiscographyCoverage, categoryID)
	if err != nil {
		return nil, err
	}
	var rows []DiscographyCoverage
	for _, figure := range figures {
		personID, err := strconv.ParseInt(figure.Key, 10, 64)
		if err != nil || figure.Count <= int64(figure.Value) {
			continue
		}
		rows = append(rows, DiscographyCoverage{PersonID: personID, Held: int64(figure.Value), Total: figure.Count})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Total-rows[i].Held > rows[j].Total-rows[j].Held
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
