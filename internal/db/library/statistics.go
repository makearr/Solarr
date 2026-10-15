package library

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// The metrics the rollup writes into stat_rollup (period 0, the current snapshot). A row's dimension is
// "<category id>|<key>"; value and count are what each says below.
const (
	// StatFilesByCodec: value bytes, count files, key the audio codec.
	StatFilesByCodec = "files_by_codec"
	// The video breakdowns of a TV library: value bytes, count files, key the measured value.
	StatFilesByVideoCodec = "files_by_video_codec"
	StatFilesByResolution = "files_by_resolution"
	StatFilesByHDR        = "files_by_hdr"
	StatFilesByAudio      = "files_by_audio"
	// StatSeriesSize: a video library's biggest series, key "<work id>|<title>", value bytes, count files.
	StatSeriesSize = "series_size"
	// StatSeriesGaps: series with monitored episodes still without a file, key "<work id>|<title>", value the aired
	// ones (missing), count the ones still to air.
	StatSeriesGaps = "series_gaps"
	// StatSizePerHour: key "<resolution> <codec>", value bytes per hour of picture, count files.
	StatSizePerHour = "size_per_hour"
	// StatVideoBitrate: the same keys, value the average video bitrate in kbit/s, count files.
	StatVideoBitrate = "video_bitrate"
	// StatAlbumsByState: value and count the monitored albums, key their desired state.
	StatAlbumsByState = "albums_by_state"
	// StatDownloadsByState: value and count the downloads of the last 30 days, key their state.
	StatDownloadsByState = "downloads_by_state"
	// StatGrowthByMonth: value bytes, count files first seen in the month, key "YYYY-MM".
	StatGrowthByMonth = "growth_by_month"
	// StatDecisionsByOutcome: value and count the release decisions of the last 30 days, key ACCEPT, REJECT or DEFER;
	// counted in statistics.db, where the decisions are.
	StatDecisionsByOutcome = "decisions_by_outcome"
	// StatSearches: value and count the searches of the last 30 days (key "searched") and those that found a release
	// the library accepts (key "found"); counted in statistics.db, where the search log is.
	StatSearches = "searches"
	// StatMostSearched: the wanted items searched most that are still missing, key "<work id>|<title>", value the
	// searches, count when the last ran (Unix seconds).
	StatMostSearched = "most_searched"
)

// The keys of StatSearches.
const (
	SearchesKeySearched = "searched"
	SearchesKeyFound    = "found"
)

// topSeries is how many series the size and gap lists keep per library; topSearched the most-searched list.
const (
	topSeries   = 20
	topSearched = 10
)

// MostSearchedKey is a StatMostSearched key: "<work id>|<media ref>|<lead>|<title>", the title last as it may hold "|".
func MostSearchedKey(workID int64, mediaRef, lead, title string) string {
	return fmt.Sprintf("%d|%s|%s|%s", workID, mediaRef, strings.ReplaceAll(lead, "|", "/"), title)
}

// StatisticsWindowDays is how far back the download figures reach.
const StatisticsWindowDays = 30

// Statistics is section 21.12's rollup: a background job computes it, the statistics page only reads it.
type Statistics struct{ store *store.Store }

// NewStatistics builds it over an open library store.
func NewStatistics(opened *store.Store) Statistics { return Statistics{store: opened} }

// StatRow is one figure the rollup stores in statistics.db: a metric's value for a dimension "<category id>|<key>".
type StatRow struct {
	Metric, Dimension string
	Value             float64
	Count             int64
}

// ComputedMetrics are the metrics Figures answers, which a rollup replaces whole.
var ComputedMetrics = []string{
	StatSeriesSize, StatSeriesGaps, StatSizePerHour, StatVideoBitrate, StatFilesByCodec, StatFilesByVideoCodec,
	StatFilesByResolution, StatFilesByHDR, StatFilesByAudio, StatAlbumsByState, StatDownloadsByState, StatGrowthByMonth,
	StatMostSearched,
}

// Figures measures every computed metric from library.db; the caller stores them in statistics.db. categoryOfRoot
// says which library each root folder belongs to (settings.db owns that, so the caller composes it); a root in none
// is left out.
func (s Statistics) Figures(ctx context.Context, categoryOfRoot map[int64]int64, now int64) ([]StatRow, error) {
	reader := librarydb.New(s.store.Read())
	codecs, err := reader.AggregateFilesByRootAndCodec(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: files: %w", err)
	}
	albums, err := reader.AggregateAlbumsByCategoryAndState(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: albums: %w", err)
	}
	downloads, err := reader.AggregateAcquisitionsByCategoryAndState(ctx, now-StatisticsWindowDays*86_400)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: downloads: %w", err)
	}

	added, err := reader.AggregateFilesAddedByRootAndMonth(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: growth: %w", err)
	}

	video, err := reader.AggregateFilesByRootAndVideo(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: video: %w", err)
	}

	seriesSize, err := reader.AggregateSeriesSize(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: series size: %w", err)
	}
	seriesGaps, err := reader.AggregateSeriesGaps(ctx, sql.NullString{String: time.Unix(now, 0).UTC().Format("2006-01-02"), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("library: statistics: series gaps: %w", err)
	}
	perHour, err := reader.AggregateSizePerHour(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: size per hour: %w", err)
	}
	mostSearched, err := reader.ListMostSearchedMissing(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: statistics: most searched: %w", err)
	}

	type figure struct {
		metric, dimension string
		value             float64
		count             int64
	}
	var figures []figure
	taken := map[string]int{}
	for _, row := range seriesSize {
		if key := dimensionOf(row.CategoryID, fmt.Sprintf("%d|%s", row.WorkID, row.Title)); taken[StatSeriesSize+strconv.FormatInt(row.CategoryID, 10)] < topSeries {
			taken[StatSeriesSize+strconv.FormatInt(row.CategoryID, 10)]++
			figures = append(figures, figure{StatSeriesSize, key, float64(row.Bytes), row.Files})
		}
	}
	for _, row := range seriesGaps {
		if key := dimensionOf(row.CategoryID, fmt.Sprintf("%d|%s", row.WorkID, row.Title)); taken[StatSeriesGaps+strconv.FormatInt(row.CategoryID, 10)] < topSeries {
			taken[StatSeriesGaps+strconv.FormatInt(row.CategoryID, 10)]++
			figures = append(figures, figure{StatSeriesGaps, key, float64(row.Missing), row.Unaired})
		}
	}
	for _, row := range mostSearched {
		if taken[StatMostSearched+strconv.FormatInt(row.CategoryID, 10)] < topSearched {
			taken[StatMostSearched+strconv.FormatInt(row.CategoryID, 10)]++
			key := dimensionOf(row.CategoryID, MostSearchedKey(row.WorkID, row.MediaRef, row.LeadName, row.Title))
			figures = append(figures, figure{StatMostSearched, key, float64(row.SearchCount), row.LastSearchedAt})
		}
	}
	type pictureTotal struct{ bytes, duration, files, bitrateSum int64 }
	pictures := map[string]*pictureTotal{}
	for _, row := range perHour {
		category, found := categoryOfRoot[row.RootFolderID]
		if !found {
			continue
		}
		key := dimensionOf(category, row.Resolution+" "+row.VideoCodec)
		total := pictures[key]
		if total == nil {
			total = &pictureTotal{}
			pictures[key] = total
		}
		total.bytes += row.Bytes
		total.duration += row.DurationMs
		total.files += row.Files
		total.bitrateSum += row.VideoBitrate * row.Files
	}
	for key, total := range pictures {
		if total.duration > 0 && total.files > 0 {
			figures = append(figures,
				figure{StatSizePerHour, key, float64(total.bytes) / (float64(total.duration) / 3_600_000), total.files},
				figure{StatVideoBitrate, key, float64(total.bitrateSum) / float64(total.files) / 1000, total.files})
		}
	}
	byVideo := map[string]*figure{}
	addVideo := func(metric string, category int64, key string, files, bytes int64) {
		dimension := metric + "/" + dimensionOf(category, key)
		if existing, ok := byVideo[dimension]; ok {
			existing.value += float64(bytes)
			existing.count += files
			return
		}
		byVideo[dimension] = &figure{metric, dimensionOf(category, key), float64(bytes), files}
	}
	for _, row := range video {
		category, found := categoryOfRoot[row.RootFolderID]
		if !found {
			continue
		}
		addVideo(StatFilesByVideoCodec, category, row.VideoCodec, row.Files, row.Bytes)
		addVideo(StatFilesByResolution, category, row.Resolution, row.Files, row.Bytes)
		addVideo(StatFilesByHDR, category, row.Hdr, row.Files, row.Bytes)
		audio := row.AudioCodec
		if row.AudioChannels != "" {
			audio += " " + row.AudioChannels
		}
		addVideo(StatFilesByAudio, category, audio, row.Files, row.Bytes)
	}
	for _, one := range byVideo {
		figures = append(figures, *one)
	}
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
	for _, row := range albums {
		figures = append(figures, figure{StatAlbumsByState, dimensionOf(row.CategoryID, row.State), float64(row.Albums), row.Albums})
	}
	for _, row := range downloads {
		figures = append(figures, figure{StatDownloadsByState, dimensionOf(row.CategoryID, row.State), float64(row.Downloads), row.Downloads})
	}

	rows := make([]StatRow, len(figures))
	for i, one := range figures {
		rows[i] = StatRow{Metric: one.metric, Dimension: one.dimension, Value: one.value, Count: one.count}
	}
	return rows, nil
}

func dimensionOf(categoryID int64, key string) string {
	return strconv.FormatInt(categoryID, 10) + "|" + key
}

// StoredRow is one row of library.db's own rollup, kept until statistics.db has taken it over (revision 2, D7).
type StoredRow struct {
	StatRow
	ComputedAt int64
}

// StoredRollup answers every current figure library.db's rollup holds, for statistics.db's first start to copy.
func (s Statistics) StoredRollup(ctx context.Context) ([]StoredRow, error) {
	rows, err := librarydb.New(s.store.Read()).ListAllStatRollup(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: stored rollup: %w", err)
	}
	var stored []StoredRow
	for _, row := range rows {
		if row.Period == 0 {
			stored = append(stored, StoredRow{StatRow{row.Metric, row.Dimension, row.Value, row.Count.Int64}, row.ComputedAt})
		}
	}
	return stored, nil
}

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
