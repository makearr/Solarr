package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrSeriesNotFound reports a series id that is not a TV series visible in the category asked about.
var ErrSeriesNotFound = errors.New("library: series not found")

// SeriesCounts are the episode counts of a series or a season. Present counts monitored episodes with a file.
type SeriesCounts struct {
	Total, Monitored, Present, AiredMissing int64
}

// SeasonSummary is one season item of a series.
type SeasonSummary struct {
	Number        int
	DesiredItemID int64
	ProfileID     int64
	Monitored     bool
	Counts        SeriesCounts
}

// SeriesSummary is a series as a list row shows it.
type SeriesSummary struct {
	WorkID      int64
	Title       string
	SortTitle   string
	Year        int64
	Description string
	AddedAt     int64
	// Fields are the merged provider fields (status, network, air_time, ...): a person's own value, else the first provider's.
	Fields map[string]string
	Genres []string
	// Seasons lists regular seasons then specials; Counts leave specials out.
	Seasons       []SeasonSummary
	Counts        SeriesCounts
	NextAvailable int64
	ProfileID     int64
	Monitored     bool
}

// SeriesEpisode is one part of a series with its coverage.
type SeriesEpisode struct {
	// File is the first file that covers the episode, with what the probe measured; nil without one.
	File                *EpisodeFile
	PartID              int64
	Season, Number      int
	Title               string
	Date                string
	RuntimeMs           int64
	Monitored           bool
	Present             bool
	AvailableAt         int64
	AvailableConfidence string
	Absolute            string
}

// SeriesCreditRow is one person's credit on a series; PartID is the episode of an episode-scoped credit, 0 for the series.
type SeriesCreditRow struct {
	PersonID  int64
	Name      string
	ImageURL  string
	Role      string
	Character string
	Order     int64
	PartID    int64
}

// SeriesDetailRow is a series with every episode, alias and external id.
type SeriesDetailRow struct {
	Summary   SeriesSummary
	Aliases   []string
	Episodes  []SeriesEpisode
	IDs       map[string]string
	Language  string
	RuntimeMs int64
	Credits   []SeriesCreditRow
}

// SeriesCursor is where the next page of a series list starts.
type SeriesCursor struct {
	SortTitle string
	ID        int64
}

// SeriesReader answers the TV screens' questions.
type SeriesReader struct{ store *store.Store }

// NewSeriesReader builds it over an open library store.
func NewSeriesReader(opened *store.Store) SeriesReader { return SeriesReader{store: opened} }

// fieldPriority orders whose value of a field wins: a person's own, then the providers in merge order.
var fieldPriority = []string{"__manual__", "skyhook", "tvmaze"}

// List returns up to pageSize series of one category after the cursor, with counts as of now.
func (r SeriesReader) List(ctx context.Context, categoryID int64, after SeriesCursor, pageSize int64, now time.Time) ([]SeriesSummary, error) {
	queries := librarydb.New(r.store.Read())
	rows, err := queries.ListSeriesInCategory(ctx, librarydb.ListSeriesInCategoryParams{
		CategoryID: categoryID, AfterSort: after.SortTitle, AfterID: after.ID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list series: %w", err)
	}
	summaries := make([]SeriesSummary, len(rows))
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		summaries[i] = SeriesSummary{
			WorkID: row.ID, Title: row.CanonicalTitle, SortTitle: row.SortTitle, Year: row.Year.Int64,
			Description: row.Description.String, AddedAt: row.AddedAt,
		}
	}
	if err := r.fill(ctx, queries, summaries, ids, now); err != nil {
		return nil, err
	}
	return summaries, nil
}

// Get returns one series of a category with every episode; ErrSeriesNotFound when it is not there.
func (r SeriesReader) Get(ctx context.Context, categoryID, workID int64, now time.Time) (SeriesDetailRow, error) {
	queries := librarydb.New(r.store.Read())
	row, err := queries.GetSeriesInCategory(ctx, librarydb.GetSeriesInCategoryParams{ID: workID, CategoryID: categoryID})
	if errors.Is(err, sql.ErrNoRows) {
		return SeriesDetailRow{}, ErrSeriesNotFound
	}
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: get series: %w", err)
	}
	summaries := []SeriesSummary{{
		WorkID: row.ID, Title: row.CanonicalTitle, SortTitle: row.SortTitle, Year: row.Year.Int64,
		Description: row.Description.String, AddedAt: row.AddedAt,
	}}
	if err := r.fill(ctx, queries, summaries, []int64{workID}, now); err != nil {
		return SeriesDetailRow{}, err
	}
	detail := SeriesDetailRow{Summary: summaries[0], Language: row.OriginalLanguage.String, RuntimeMs: row.RuntimeMs.Int64, IDs: map[string]string{}}

	externals, err := queries.ListWorkExternalIDs(ctx, workID)
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: series ids: %w", err)
	}
	for _, external := range externals {
		if _, have := detail.IDs[external.Provider]; !have {
			detail.IDs[external.Provider] = external.ExternalID
		}
	}
	titles, err := queries.ListWorkTitles(ctx, workID)
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: series aliases: %w", err)
	}
	for _, title := range titles {
		detail.Aliases = append(detail.Aliases, title.Title)
	}

	if detail.Credits, err = r.credits(ctx, workID); err != nil {
		return SeriesDetailRow{}, err
	}
	parts, err := queries.ListWorkParts(ctx, workID)
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: series parts: %w", err)
	}
	covered, err := queries.CoveredPartRefs(ctx, workID)
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: covered parts: %w", err)
	}
	hasFile := make(map[string]bool, len(covered))
	for _, ref := range covered {
		hasFile[ref] = true
	}
	absolutes, err := queries.ListAltNumbersForWork(ctx, librarydb.ListAltNumbersForWorkParams{WorkID: workID, Scheme: "absolute"})
	if err != nil {
		return SeriesDetailRow{}, fmt.Errorf("library: absolute numbers: %w", err)
	}
	absoluteOf := make(map[int64]string, len(absolutes))
	for _, alt := range absolutes {
		absoluteOf[alt.PartID] = alt.AltRef
	}
	files, err := r.episodeFiles(ctx, workID)
	if err != nil {
		return SeriesDetailRow{}, err
	}
	for _, part := range parts {
		season, number := parseSeriesRef(part.MediaRef)
		file := files[part.MediaRef]
		detail.Episodes = append(detail.Episodes, SeriesEpisode{File: file,
			PartID: part.ID, Season: season, Number: number, Absolute: absoluteOf[part.ID], Title: part.Title.String, Date: part.Date.String,
			RuntimeMs: part.RuntimeMs.Int64, Monitored: part.Monitored == 1, Present: hasFile[part.MediaRef],
			AvailableAt: part.AvailableAt.Int64, AvailableConfidence: part.AvailableConfidence,
		})
	}
	return detail, nil
}

func (r SeriesReader) credits(ctx context.Context, workID int64) ([]SeriesCreditRow, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT c.person_id, p.name, COALESCE(json_extract(p.fields_json, '$.image_url'), ''), c.role, c.character, COALESCE(c.ord, 0), c.part_id
FROM credit c JOIN person p ON p.id = c.person_id
WHERE c.work_id = ? ORDER BY c.part_id != 0, c.ord, p.name COLLATE NOCASE, p.id`, workID)
	if err != nil {
		return nil, fmt.Errorf("library: series credits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var credits []SeriesCreditRow
	for rows.Next() {
		var credit SeriesCreditRow
		if err := rows.Scan(&credit.PersonID, &credit.Name, &credit.ImageURL, &credit.Role, &credit.Character, &credit.Order, &credit.PartID); err != nil {
			return nil, fmt.Errorf("library: scan series credit: %w", err)
		}
		credits = append(credits, credit)
	}
	return credits, rows.Err()
}

func (r SeriesReader) fill(ctx context.Context, queries *librarydb.Queries, summaries []SeriesSummary, ids []int64, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	counts, err := queries.SeriesPartCounts(ctx, librarydb.SeriesPartCountsParams{
		Today: sql.NullString{String: now.UTC().Format("2006-01-02"), Valid: true}, Now: sql.NullInt64{Int64: now.Unix(), Valid: true}, WorkIds: ids,
	})
	if err != nil {
		return fmt.Errorf("library: series counts: %w", err)
	}
	countsByWork := make(map[int64]librarydb.SeriesPartCountsRow, len(counts))
	for _, row := range counts {
		countsByWork[row.WorkID] = row
	}
	items, err := queries.SeasonItemsForWorks(ctx, ids)
	if err != nil {
		return fmt.Errorf("library: season items: %w", err)
	}
	itemsByWork := map[int64][]librarydb.SeasonItemsForWorksRow{}
	for _, item := range items {
		itemsByWork[item.WorkID] = append(itemsByWork[item.WorkID], item)
	}

	for i := range summaries {
		summary := &summaries[i]
		row := countsByWork[summary.WorkID]
		summary.NextAvailable = row.NextAvailable
		summary.Counts.Total = row.Total
		var present int64
		for _, item := range itemsByWork[summary.WorkID] {
			number, _ := parseSeriesRef(item.MediaRef + ":")
			season := SeasonSummary{
				Number: number, DesiredItemID: item.ID, ProfileID: item.ProfileID, Monitored: item.Monitored == 1,
				Counts: SeriesCounts{Monitored: item.RequiredCount.Int64, Present: item.PresentCount},
			}
			summary.Seasons = append(summary.Seasons, season)
			if number == 0 {
				continue
			}
			present += item.PresentCount
			summary.Monitored = summary.Monitored || season.Monitored
			if summary.ProfileID == 0 {
				summary.ProfileID = item.ProfileID
			}
		}
		summary.Counts.Monitored = row.Monitored
		summary.Counts.Present = present
		summary.Counts.AiredMissing = max(0, row.MonitoredAired-present)

		fields, err := queries.ListWorkFields(ctx, summary.WorkID)
		if err != nil {
			return fmt.Errorf("library: series fields: %w", err)
		}
		summary.Fields = mergeFields(fields)
		classifications, err := queries.ListWorkClassifications(ctx, summary.WorkID)
		if err != nil {
			return fmt.Errorf("library: series genres: %w", err)
		}
		for _, classification := range classifications {
			if classification.Kind == "genre" {
				summary.Genres = append(summary.Genres, classification.Value)
			}
		}
	}
	return nil
}

// mergeFields picks one value per field: a person's own, then the first provider in fieldPriority, then any.
func mergeFields(fields []librarydb.WorkField) map[string]string {
	best := map[string]int{}
	merged := map[string]string{}
	for _, field := range fields {
		if !field.Value.Valid {
			continue
		}
		rank := len(fieldPriority)
		for i, provider := range fieldPriority {
			if provider == field.Provider {
				rank = i
			}
		}
		if have, found := best[field.Field]; !found || rank < have {
			best[field.Field] = rank
			merged[field.Field] = field.Value.String
		}
	}
	return merged
}

// parseSeriesRef reads "s03:e10" (or a season item's "s03:") into season and episode; zero when absent.
func parseSeriesRef(ref string) (season, episode int) {
	head, tail, _ := strings.Cut(ref, ":")
	season, _ = strconv.Atoi(strings.TrimPrefix(head, "s"))
	episode, _ = strconv.Atoi(strings.TrimPrefix(tail, "e"))
	return season, episode
}

// InCategory reports whether the series is a TV series held by the category.
func (r SeriesReader) InCategory(ctx context.Context, categoryID, workID int64) error {
	_, err := librarydb.New(r.store.Read()).GetSeriesInCategory(ctx, librarydb.GetSeriesInCategoryParams{ID: workID, CategoryID: categoryID})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSeriesNotFound
	}
	return err
}

// ProviderIDs answers a series' provider ids (tvdb, tvmaze, ...), for a refresh that must find it again.
func (r SeriesReader) ProviderIDs(ctx context.Context, workID int64) (map[string]string, error) {
	rows, err := librarydb.New(r.store.Read()).ListWorkExternalIDs(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: series ids: %w", err)
	}
	ids := make(map[string]string, len(rows))
	for _, row := range rows {
		if _, have := ids[row.Provider]; !have {
			ids[row.Provider] = row.ExternalID
		}
	}
	return ids, nil
}

// SeriesTitle is one name a series answers to.
type SeriesTitle struct {
	WorkID int64
	Title  string
	Year   int64
}

// Titles lists every canonical and alias title of a category's series.
func (r SeriesReader) Titles(ctx context.Context, categoryID int64) ([]SeriesTitle, error) {
	rows, err := librarydb.New(r.store.Read()).ListSeriesTitles(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: series titles: %w", err)
	}
	titles := make([]SeriesTitle, len(rows))
	for i, row := range rows {
		titles[i] = SeriesTitle{WorkID: row.WorkID, Title: row.Title, Year: row.Year.Int64}
	}
	return titles, nil
}

// WantedEpisode is a monitored episode that has aired and has no file.
type WantedEpisode struct {
	PartID      int64
	WorkID      int64
	SeriesTitle string
	Season      int
	Number      int
	Title       string
	Date        string
	AvailableAt int64
}

// WantedCursor is where the next page of the missing list starts; the zero value is the first page.
type WantedCursor struct {
	Date string
	ID   int64
}

// Wanted lists a category's missing episodes, newest first, up to pageSize after the cursor.
func (r SeriesReader) Wanted(ctx context.Context, categoryID int64, after WantedCursor, pageSize int64, now time.Time) ([]WantedEpisode, error) {
	afterDate := after.Date
	if afterDate == "" {
		afterDate = "9999-99-99"
	}
	afterID := after.ID
	if after.Date == "" {
		afterID = 1<<62 - 1
	}
	rows, err := librarydb.New(r.store.Read()).ListWantedEpisodes(ctx, librarydb.ListWantedEpisodesParams{
		CategoryID: categoryID, Now: sql.NullInt64{Int64: now.Unix(), Valid: true},
		Today:     sql.NullString{String: now.UTC().Format("2006-01-02"), Valid: true},
		AfterDate: sql.NullString{String: afterDate, Valid: true}, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: wanted episodes: %w", err)
	}
	episodes := make([]WantedEpisode, len(rows))
	for i, row := range rows {
		season, number := parseSeriesRef(row.MediaRef)
		episodes[i] = WantedEpisode{
			PartID: row.PartID, WorkID: row.WorkID, SeriesTitle: row.SeriesTitle, Season: season, Number: number,
			Title: row.Title.String, Date: row.Date.String, AvailableAt: row.AvailableAt.Int64,
		}
	}
	return episodes, nil
}

// SeasonMonitoring answers each season item's monitored flag, by season number (specials are 0).
func (r SeriesReader) SeasonMonitoring(ctx context.Context, workID int64) (map[int]bool, error) {
	items, err := librarydb.New(r.store.Read()).ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: season items: %w", err)
	}
	monitored := make(map[int]bool, len(items))
	for _, item := range items {
		if item.SatisfactionKind == "SET" && item.MediaRef != "" {
			season, _ := parseSeriesRef(item.MediaRef + ":")
			monitored[season] = item.Monitored == 1
		}
	}
	return monitored, nil
}

// RefreshCandidate is a series whose providers have not been asked lately.
type RefreshCandidate struct {
	WorkID     int64
	CategoryID int64
}

// DueForRefresh lists the series last written longer ago than their status asks: those not known to have ended after
// continuing, an ended one after endedAfter. The oldest come first.
func (r SeriesReader) DueForRefresh(ctx context.Context, now time.Time, continuing, ended time.Duration, limit int) ([]RefreshCandidate, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT w.id, COALESCE((SELECT d.category_id FROM desired_item d WHERE d.work_id = w.id ORDER BY d.id LIMIT 1), 0)
FROM work w
WHERE w.media_type = 'tv'
  AND w.updated_at < ? - CASE WHEN COALESCE((SELECT f.value FROM work_field f WHERE f.work_id = w.id AND f.field = 'status'
                                             ORDER BY f.provider = '__manual__' DESC LIMIT 1), '') = 'ended' THEN ? ELSE ? END
ORDER BY w.updated_at, w.id LIMIT ?`, now.Unix(), int64(ended.Seconds()), int64(continuing.Seconds()), limit)
	if err != nil {
		return nil, fmt.Errorf("library: series due for refresh: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var due []RefreshCandidate
	for rows.Next() {
		var candidate RefreshCandidate
		if err := rows.Scan(&candidate.WorkID, &candidate.CategoryID); err != nil {
			return nil, fmt.Errorf("library: scan refresh candidate: %w", err)
		}
		due = append(due, candidate)
	}
	return due, rows.Err()
}
