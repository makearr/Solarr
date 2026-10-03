package library

import (
	"context"
	"fmt"
)

// SeasonFile is one file behind an episode of a season; an episode an upgrade is replacing has two.
type SeasonFile struct {
	Ref       string
	Episode   int
	Monitored bool
	// Measured is false until the probe has read the video.
	Measured bool
	// AddedAt is when the file was attached to the episode.
	AddedAt int64
	File    EpisodeFile
}

// SeasonFiles lists every file of every episode of a season item, the incumbent (lowest media object id) first.
func (r SeriesReader) SeasonFiles(ctx context.Context, itemID int64) ([]SeasonFile, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT f.media_ref, p.monitored, f.added_at, mo.id, mo.size_bytes, COALESCE(ma.resolution, ''), COALESCE(ma.video_codec, ''), COALESCE(ma.hdr, ''),
       COALESCE(ma.audio_codec, ''), COALESCE(ma.audio_channels, ''), COALESCE(ma.subtitles, ''),
       COALESCE(mo.video_bitrate, 0), COALESCE(mo.duration_ms, 0), COALESCE(ma.container, ''),
       COALESCE(ma.languages, ''), COALESCE(ma.frame_rate, 0), COALESCE(mo.audio_bitrate, 0)
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
JOIN work_part p ON p.work_id = d.work_id AND p.media_ref = f.media_ref
JOIN media_object mo ON mo.id = f.media_object_id
LEFT JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE f.desired_item_id = ? AND f.role = 'primary' AND f.media_ref <> ''
ORDER BY f.media_ref, mo.id`, itemID)
	if err != nil {
		return nil, fmt.Errorf("library: season files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var files []SeasonFile
	for rows.Next() {
		var found SeasonFile
		var subtitles, languages string
		var monitored int64
		if err := rows.Scan(&found.Ref, &monitored, &found.AddedAt, &found.File.MediaObjectID, &found.File.SizeBytes, &found.File.Resolution, &found.File.VideoCodec,
			&found.File.HDR, &found.File.AudioCodec, &found.File.AudioChannels, &subtitles, &found.File.VideoBitrate, &found.File.DurationMs,
			&found.File.Container, &languages, &found.File.FrameRate, &found.File.AudioBitrate); err != nil {
			return nil, fmt.Errorf("library: scan season file: %w", err)
		}
		found.Monitored = monitored == 1
		found.Measured = found.File.VideoCodec != ""
		_, found.Episode = parseSeriesRef(found.Ref)
		found.File.Subtitles = jsonStrings(nullString(subtitles))
		found.File.Languages = jsonStrings(nullString(languages))
		files = append(files, found)
	}
	return files, rows.Err()
}

// TVCutoffPage pages through monitored COMPLETE, UPGRADABLE and PARTIAL season items of TV series by id.
func (d Desired) TVCutoffPage(ctx context.Context, afterID, limit int64) ([]CutoffItem, error) {
	rows, err := d.store.Read().QueryContext(ctx,
		`SELECT id, work_id, category_id, profile_id, state, present_count FROM desired_item
WHERE monitored = 1 AND satisfaction_kind = 'SET' AND state IN ('COMPLETE', 'UPGRADABLE', 'PARTIAL') AND id > ?
  AND EXISTS (SELECT 1 FROM work w WHERE w.id = desired_item.work_id AND w.media_type = 'tv')
ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("library: tv cutoff page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var items []CutoffItem
	for rows.Next() {
		var item CutoffItem
		if err := rows.Scan(&item.ID, &item.WorkID, &item.CategoryID, &item.ProfileID, &item.State, &item.PresentCount); err != nil {
			return nil, fmt.Errorf("library: scan tv cutoff item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
