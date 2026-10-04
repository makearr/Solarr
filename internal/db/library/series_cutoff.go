package library

import (
	"context"
	"fmt"
)

// HeldEpisode is a present episode of a library with the file behind it, for judging it against the library's quality.
type HeldEpisode struct {
	PartID, WorkID int64
	SeriesTitle    string
	Season, Number int
	Title, Date    string
	File           EpisodeFile
}

// HeldEpisodes lists every episode of a library that has a measured file, by series title, season and episode.
func (r SeriesReader) HeldEpisodes(ctx context.Context, categoryID int64) ([]HeldEpisode, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT p.id, w.id, w.canonical_title, p.media_ref, COALESCE(p.title, ''), COALESCE(p.date, ''),
       mo.id, mo.size_bytes, COALESCE(ma.resolution, ''), COALESCE(ma.video_codec, ''), COALESCE(ma.hdr, ''),
       COALESCE(ma.audio_codec, ''), COALESCE(ma.audio_channels, ''), COALESCE(ma.subtitles, ''),
       COALESCE(mo.video_bitrate, 0), COALESCE(ma.container, ''),
       COALESCE(ma.languages, ''), COALESCE(ma.frame_rate, 0), COALESCE(mo.audio_bitrate, 0), COALESCE(ma.crf, 0)
FROM work w
JOIN work_part p ON p.work_id = w.id
JOIN desired_item_file f ON f.media_ref = p.media_ref AND f.role = 'primary'
JOIN desired_item d ON d.id = f.desired_item_id AND d.work_id = w.id AND d.category_id = ?
JOIN media_object mo ON mo.id = f.media_object_id
JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE w.media_type = 'tv' AND ma.video_codec IS NOT NULL AND ma.video_codec <> ''
GROUP BY p.id
ORDER BY w.sort_title COLLATE NOCASE, w.id, p.sort_key, p.id`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: held episodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var held []HeldEpisode
	for rows.Next() {
		var episode HeldEpisode
		var ref, subtitles, languages string
		if err := rows.Scan(&episode.PartID, &episode.WorkID, &episode.SeriesTitle, &ref, &episode.Title, &episode.Date,
			&episode.File.MediaObjectID, &episode.File.SizeBytes, &episode.File.Resolution, &episode.File.VideoCodec, &episode.File.HDR,
			&episode.File.AudioCodec, &episode.File.AudioChannels, &subtitles, &episode.File.VideoBitrate, &episode.File.Container,
			&languages, &episode.File.FrameRate, &episode.File.AudioBitrate, &episode.File.CRF); err != nil {
			return nil, fmt.Errorf("library: scan held episode: %w", err)
		}
		episode.Season, episode.Number = parseSeriesRef(ref)
		episode.File.Subtitles = jsonStrings(nullString(subtitles))
		episode.File.Languages = jsonStrings(nullString(languages))
		held = append(held, episode)
	}
	return held, rows.Err()
}
