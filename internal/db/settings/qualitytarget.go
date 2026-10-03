package settings

import (
	"database/sql"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
)

// splitList reads a comma separated column, empty entries dropped.
func splitList(text string) []string {
	var out []string
	for _, part := range strings.Split(text, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinList(values []string) string { return strings.Join(values, ",") }

func floatToNull(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: value, Valid: value > 0}
}

// targetFromRow is a stored target as the model.
func targetFromRow(row settingsdb.CategoryQualityTarget) *QualityTarget {
	return &QualityTarget{
		AudioCodec: row.AudioCodec, Container: row.Container, BitrateKbps: nullInt64Ptr(row.BitrateKbps), TolerancePct: row.TolerancePct,
		Resolution: row.Resolution, VideoCodec: row.VideoCodec, HDR: row.Hdr,
		AudioChannels: row.AudioChannels, AudioLanguages: splitList(row.AudioLanguages), SubtitleLanguages: splitList(row.SubtitleLanguages),
		AudioBitrateKbps: nullInt64Ptr(row.AudioBitrateKbps), VideoBitrateKbps: nullInt64Ptr(row.VideoBitrateKbps), FrameRate: row.FrameRate.Float64,
	}
}

// upsertParams is the target as the row a library's target is stored in.
func (t QualityTarget) upsertParams(categoryID int64) settingsdb.UpsertCategoryQualityTargetParams {
	return settingsdb.UpsertCategoryQualityTargetParams{
		CategoryID: categoryID, AudioCodec: t.AudioCodec, Container: t.Container, BitrateKbps: int64PtrToNull(t.BitrateKbps),
		TolerancePct: t.TolerancePct, Resolution: t.Resolution, VideoCodec: t.VideoCodec, Hdr: t.HDR,
		AudioChannels: t.AudioChannels, AudioLanguages: joinList(t.AudioLanguages), SubtitleLanguages: joinList(t.SubtitleLanguages),
		AudioBitrateKbps: int64PtrToNull(t.AudioBitrateKbps), VideoBitrateKbps: int64PtrToNull(t.VideoBitrateKbps), FrameRate: floatToNull(t.FrameRate),
	}
}

// entry is the target as the exported document keeps it.
func (t QualityTarget) entry() *QualityTargetEntry {
	return &QualityTargetEntry{
		AudioCodec: t.AudioCodec, Container: t.Container, BitrateKbps: t.BitrateKbps, TolerancePct: t.TolerancePct,
		Resolution: t.Resolution, VideoCodec: t.VideoCodec, HDR: t.HDR, AudioChannels: t.AudioChannels,
		AudioLanguages: t.AudioLanguages, SubtitleLanguages: t.SubtitleLanguages,
		AudioBitrateKbps: t.AudioBitrateKbps, VideoBitrateKbps: t.VideoBitrateKbps, FrameRate: t.FrameRate,
	}
}

// target is an exported document's entry as the model.
func (e QualityTargetEntry) target() QualityTarget { return QualityTarget(e) }

// IsVideo says the target names a picture, a frame rate or a sound layout: a video library's aim, not an audio
// codec and file type.
func (t QualityTarget) IsVideo() bool {
	return t.Resolution != "" || t.VideoCodec != "" || t.HDR != "" || t.FrameRate > 0 || t.VideoBitrateKbps != nil ||
		t.AudioChannels != "" || len(t.AudioLanguages) > 0 || len(t.SubtitleLanguages) > 0 || t.AudioBitrateKbps != nil
}
