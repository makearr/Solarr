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

func positiveToNull(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: value > 0}
}

func floatToNull(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: value, Valid: value > 0}
}

// targetFromRow is a stored target as the model.
func targetFromRow(row settingsdb.QualityTarget) QualityTarget {
	return QualityTarget{
		ID: row.ID, Name: row.Name, MediaType: row.MediaType,
		AudioCodec: row.AudioCodec, Container: row.Container, BitrateKbps: nullInt64Ptr(row.BitrateKbps), TolerancePct: row.TolerancePct,
		Resolution: row.Resolution, VideoCodec: row.VideoCodec, HDR: row.Hdr,
		AudioChannels: row.AudioChannels, AudioLanguages: splitList(row.AudioLanguages), SubtitleLanguages: splitList(row.SubtitleLanguages),
		AudioBitrateKbps: nullInt64Ptr(row.AudioBitrateKbps), VideoBitrateKbps: nullInt64Ptr(row.VideoBitrateKbps), FrameRate: row.FrameRate.Float64,
		CRF: row.Crf.Float64, PreferOver: row.PreferOver == 1, IgnoreBelow: row.IgnoreBelow == 1,
		Label: row.Label, Description: row.Description, SampleRateHz: row.SampleRateHz.Int64, BitDepth: row.BitDepth.Int64,
		EncoderPreset: row.EncoderPreset, MaxFrameRate: row.MaxFrameRate.Float64, Chapters: row.Chapters == 1, Builtin: row.Builtin == 1,
		ProfileID: nullInt64Ptr(row.ProfileID), EncoderTune: row.EncoderTune, Deinterlace: row.Deinterlace == 1,
	}
}

// createParams is the target as a new row.
func (t QualityTarget) createParams() settingsdb.CreateQualityTargetParams {
	return settingsdb.CreateQualityTargetParams{
		Name: t.Name, MediaType: t.MediaType, AudioCodec: t.AudioCodec, Container: t.Container, BitrateKbps: int64PtrToNull(t.BitrateKbps),
		TolerancePct: t.TolerancePct, Resolution: t.Resolution, VideoCodec: t.VideoCodec, Hdr: t.HDR,
		AudioChannels: t.AudioChannels, AudioLanguages: joinList(t.AudioLanguages), SubtitleLanguages: joinList(t.SubtitleLanguages),
		AudioBitrateKbps: int64PtrToNull(t.AudioBitrateKbps), VideoBitrateKbps: int64PtrToNull(t.VideoBitrateKbps),
		FrameRate: floatToNull(t.FrameRate), Crf: floatToNull(t.CRF),
		PreferOver: boolToInt(t.PreferOver), IgnoreBelow: boolToInt(t.IgnoreBelow),
		Label: t.Label, Description: t.Description, SampleRateHz: positiveToNull(t.SampleRateHz), BitDepth: positiveToNull(t.BitDepth),
		EncoderPreset: t.EncoderPreset, MaxFrameRate: floatToNull(t.MaxFrameRate), Chapters: boolToInt(t.Chapters),
		ProfileID: int64PtrToNull(t.ProfileID), EncoderTune: t.EncoderTune, Deinterlace: boolToInt(t.Deinterlace),
	}
}

// updateParams is the target as the replacement of its row.
func (t QualityTarget) updateParams() settingsdb.UpdateQualityTargetParams {
	return settingsdb.UpdateQualityTargetParams{
		ID: t.ID, Name: t.Name, AudioCodec: t.AudioCodec, Container: t.Container, BitrateKbps: int64PtrToNull(t.BitrateKbps),
		TolerancePct: t.TolerancePct, Resolution: t.Resolution, VideoCodec: t.VideoCodec, Hdr: t.HDR,
		AudioChannels: t.AudioChannels, AudioLanguages: joinList(t.AudioLanguages), SubtitleLanguages: joinList(t.SubtitleLanguages),
		AudioBitrateKbps: int64PtrToNull(t.AudioBitrateKbps), VideoBitrateKbps: int64PtrToNull(t.VideoBitrateKbps),
		FrameRate: floatToNull(t.FrameRate), Crf: floatToNull(t.CRF),
		PreferOver: boolToInt(t.PreferOver), IgnoreBelow: boolToInt(t.IgnoreBelow),
		Label: t.Label, Description: t.Description, SampleRateHz: positiveToNull(t.SampleRateHz), BitDepth: positiveToNull(t.BitDepth),
		EncoderPreset: t.EncoderPreset, MaxFrameRate: floatToNull(t.MaxFrameRate), Chapters: boolToInt(t.Chapters),
		ProfileID: int64PtrToNull(t.ProfileID), EncoderTune: t.EncoderTune, Deinterlace: boolToInt(t.Deinterlace),
	}
}

// entry is the target as the exported document keeps it.
func (t QualityTarget) entry() QualityTargetEntry {
	return QualityTargetEntry{
		Name: t.Name, MediaType: exportedMediaType(t.MediaType),
		AudioCodec: t.AudioCodec, Container: t.Container, BitrateKbps: t.BitrateKbps, TolerancePct: t.TolerancePct,
		Resolution: t.Resolution, VideoCodec: t.VideoCodec, HDR: t.HDR, AudioChannels: t.AudioChannels,
		AudioLanguages: t.AudioLanguages, SubtitleLanguages: t.SubtitleLanguages,
		AudioBitrateKbps: t.AudioBitrateKbps, VideoBitrateKbps: t.VideoBitrateKbps, FrameRate: t.FrameRate, CRF: t.CRF, PreferOver: t.PreferOver, IgnoreBelow: t.IgnoreBelow,
		Label: t.Label, Description: t.Description, SampleRateHz: t.SampleRateHz, BitDepth: t.BitDepth, EncoderPreset: t.EncoderPreset,
		MaxFrameRate: t.MaxFrameRate, Chapters: t.Chapters, EncoderTune: t.EncoderTune, Deinterlace: t.Deinterlace,
	}
}

// target is an exported document's entry as the model.
func (e QualityTargetEntry) target() QualityTarget {
	return QualityTarget{
		Name: e.Name, MediaType: e.MediaType,
		AudioCodec: e.AudioCodec, Container: e.Container, BitrateKbps: e.BitrateKbps, TolerancePct: e.TolerancePct,
		Resolution: e.Resolution, VideoCodec: e.VideoCodec, HDR: e.HDR, AudioChannels: e.AudioChannels,
		AudioLanguages: e.AudioLanguages, SubtitleLanguages: e.SubtitleLanguages,
		AudioBitrateKbps: e.AudioBitrateKbps, VideoBitrateKbps: e.VideoBitrateKbps, FrameRate: e.FrameRate, CRF: e.CRF, PreferOver: e.PreferOver, IgnoreBelow: e.IgnoreBelow,
		Label: e.Label, Description: e.Description, SampleRateHz: e.SampleRateHz, BitDepth: e.BitDepth, EncoderPreset: e.EncoderPreset,
		MaxFrameRate: e.MaxFrameRate, Chapters: e.Chapters, EncoderTune: e.EncoderTune, Deinterlace: e.Deinterlace,
	}
}

// TargetFamilies are the groups of media types that share their target qualities: the files are alike (a movie, an episode
// and a scene are all video), so one preset serves them all and each library still picks its own.
var TargetFamilies = []string{"video", "music", "spoken", "book"}

// TargetFamily is the family a media type's targets belong to; a media type of no family is its own.
func TargetFamily(mediaType string) string {
	switch mediaType {
	case "tv", "movie", "adult", "youtube", "news", "video":
		return "video"
	case "audiobook", "podcast", "spoken":
		return "spoken"
	case "book", "comic":
		return "book"
	}
	return mediaType
}

// PreferredTargetLabel tags the shipped target that holds the user's own preferred settings.
const PreferredTargetLabel = "Space conscious"

// AudioMediaTypes are the media types whose target is an audio codec and file type, whatever channel layout it names.
var AudioMediaTypes = map[string]bool{"music": true, "spoken": true, "audiobook": true, "podcast": true}

// DocumentMediaTypes are the media types whose target is a file type alone.
var DocumentMediaTypes = map[string]bool{"book": true, "comic": true}

// IsVideo says the target names a picture, a frame rate or a sound layout: a video library's aim, not an audio
// codec and file type.
func (t QualityTarget) IsVideo() bool {
	if AudioMediaTypes[t.MediaType] || DocumentMediaTypes[t.MediaType] {
		return false
	}
	return t.Resolution != "" || t.VideoCodec != "" || t.HDR != "" || t.FrameRate > 0 || t.VideoBitrateKbps != nil ||
		t.CRF > 0 || t.AudioChannels != "" || len(t.AudioLanguages) > 0 || len(t.SubtitleLanguages) > 0 || t.AudioBitrateKbps != nil
}
