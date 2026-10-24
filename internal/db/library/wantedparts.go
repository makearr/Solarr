package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// WantedPart is one part of a library's work that is wanted at its own grain (an episode): out and without a file, or
// held with a file to judge against the library's quality.
type WantedPart struct {
	PartID, WorkID      int64
	WorkTitle, MediaRef string
	Title, Date         string
	AvailableAt         int64
	// File is the held part's file, nil for a missing one.
	File *PartFile
}

// WantedPartsCursor is where the next page of missing parts starts; the zero value is the first page.
type WantedPartsCursor struct {
	Date string
	ID   int64
}

// WantedParts lists a library's missing parts, newest first, up to pageSize after the cursor.
func (w Works) WantedParts(ctx context.Context, categoryID int64, after WantedPartsCursor, pageSize int64, now time.Time) ([]WantedPart, error) {
	afterDate, afterID := after.Date, after.ID
	// The zero cursor alone is the first page: a page may end on a part with no date, which is Date "" too.
	if afterDate == "" && afterID == 0 {
		afterDate, afterID = "9999-99-99", 1<<62-1
	}
	rows, err := librarydb.New(w.store.Read()).ListWantedParts(ctx, librarydb.ListWantedPartsParams{
		CategoryID: categoryID, Now: sql.NullInt64{Int64: now.Unix(), Valid: true},
		Today:     sql.NullString{String: now.UTC().Format("2006-01-02"), Valid: true},
		AfterDate: sql.NullString{String: afterDate, Valid: true}, AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: wanted parts: %w", err)
	}
	parts := make([]WantedPart, len(rows))
	for i, row := range rows {
		parts[i] = WantedPart{
			PartID: row.PartID, WorkID: row.WorkID, WorkTitle: row.WorkTitle, MediaRef: row.MediaRef,
			Title: row.Title.String, Date: row.Date.String, AvailableAt: row.AvailableAt.Int64,
		}
	}
	return parts, nil
}

// HeldParts lists every part of a library that has a measured file, by work title and part order: what a cutoff view
// judges, in Go, against the library's quality.
func (w Works) HeldParts(ctx context.Context, categoryID int64) ([]WantedPart, error) {
	rows, err := w.store.Read().QueryContext(ctx,
		`SELECT p.id, w.id, w.canonical_title, p.media_ref, COALESCE(p.title, ''), COALESCE(p.date, ''),
       mo.id, mo.size_bytes, COALESCE(ma.resolution, ''), COALESCE(ma.video_codec, ''), COALESCE(ma.hdr, ''),
       COALESCE(ma.audio_codec, ''), COALESCE(ma.audio_channels, ''), COALESCE(ma.subtitles, ''),
       COALESCE(mo.video_bitrate, 0), COALESCE(ma.container, ''), COALESCE(ma.languages, ''),
       COALESCE(ma.frame_rate, 0), COALESCE(mo.overall_bitrate, mo.audio_bitrate, 0), COALESCE(ma.crf, 0), COALESCE(ma.lossless, 0)
FROM work w
JOIN work_part p ON p.work_id = w.id
JOIN desired_item_file f ON f.media_ref = p.media_ref AND f.role = 'primary'
JOIN desired_item d ON d.id = f.desired_item_id AND d.work_id = w.id AND d.category_id = ?
JOIN media_object mo ON mo.id = f.media_object_id
JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE (COALESCE(ma.video_codec, '') <> '' OR COALESCE(ma.audio_codec, '') <> '')
GROUP BY p.id
ORDER BY w.sort_title COLLATE NOCASE, w.id, p.sort_key, p.id`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: held parts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var held []WantedPart
	for rows.Next() {
		var part WantedPart
		var file PartFile
		var subtitles, languages string
		var videoBitrate, bitrate, lossless int64
		if err := rows.Scan(&part.PartID, &part.WorkID, &part.WorkTitle, &part.MediaRef, &part.Title, &part.Date,
			&file.MediaObjectID, &file.SizeBytes, &file.Resolution, &file.VideoCodec, &file.HDR,
			&file.AudioCodec, &file.AudioChannels, &subtitles, &videoBitrate, &file.Container, &languages,
			&file.FrameRate, &bitrate, &file.CRF, &lossless); err != nil {
			return nil, fmt.Errorf("library: scan held part: %w", err)
		}
		file.VideoBitrateKbps, file.BitrateKbps, file.Lossless = videoBitrate/1000, bitrate/1000, lossless == 1
		file.Subtitles = jsonStrings(nullString(subtitles))
		file.AudioLanguages = jsonStrings(nullString(languages))
		part.File = &file
		held = append(held, part)
	}
	return held, rows.Err()
}
