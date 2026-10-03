package library

import (
	"context"
	"fmt"
)

// EpisodeFile is the file behind an episode and what the probe measured of it; a field the probe has not read is empty.
type EpisodeFile struct {
	MediaObjectID               int64
	SizeBytes                   int64
	Resolution, VideoCodec, HDR string
	AudioCodec, AudioChannels   string
	Subtitles                   []string
	VideoBitrate, DurationMs    int64
	Container                   string
	// Languages are the audio languages the probe read; FrameRate and AudioBitrate (bit/s) are what it measured.
	Languages    []string
	FrameRate    float64
	AudioBitrate int64
}

// episodeFiles maps each covered episode (by media_ref) to its first file.
func (r SeriesReader) episodeFiles(ctx context.Context, workID int64) (map[string]*EpisodeFile, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT f.media_ref, mo.id, mo.size_bytes, COALESCE(ma.resolution, ''), COALESCE(ma.video_codec, ''), COALESCE(ma.hdr, ''),
       COALESCE(ma.audio_codec, ''), COALESCE(ma.audio_channels, ''), COALESCE(ma.subtitles, ''),
       COALESCE(mo.video_bitrate, 0), COALESCE(mo.duration_ms, 0), COALESCE(ma.container, ''),
       COALESCE(ma.languages, ''), COALESCE(ma.frame_rate, 0), COALESCE(mo.audio_bitrate, 0)
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
JOIN media_object mo ON mo.id = f.media_object_id
LEFT JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
WHERE d.work_id = ? AND f.role = 'primary' AND f.media_ref <> ''
ORDER BY f.media_ref, mo.id`, workID)
	if err != nil {
		return nil, fmt.Errorf("library: episode files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	files := map[string]*EpisodeFile{}
	for rows.Next() {
		var ref, subtitles, languages string
		var file EpisodeFile
		if err := rows.Scan(&ref, &file.MediaObjectID, &file.SizeBytes, &file.Resolution, &file.VideoCodec, &file.HDR,
			&file.AudioCodec, &file.AudioChannels, &subtitles, &file.VideoBitrate, &file.DurationMs, &file.Container,
			&languages, &file.FrameRate, &file.AudioBitrate); err != nil {
			return nil, fmt.Errorf("library: scan episode file: %w", err)
		}
		file.Subtitles = jsonStrings(nullString(subtitles))
		file.Languages = jsonStrings(nullString(languages))
		if _, have := files[ref]; !have {
			files[ref] = &file
		}
	}
	return files, rows.Err()
}
