package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/config"
)

func targetProfileFromRow(row settingsdb.TargetProfile) TargetProfile {
	return TargetProfile{
		ID: row.ID, Name: row.Name, MediaTypes: splitList(row.MediaTypes), Description: row.Description,
		Resolution: row.Resolution, VideoCodec: row.VideoCodec, FrameRate: row.FrameRate.Float64, MaxFrameRate: row.MaxFrameRate.Float64, CRF: row.Crf.Float64,
		EncoderPreset: row.EncoderPreset, EncoderTune: row.EncoderTune, Deinterlace: row.Deinterlace == 1,
		AudioCodec: row.AudioCodec, AudioChannels: row.AudioChannels, AudioBitrateKbps: nullInt64Ptr(row.AudioBitrateKbps),
		SampleRateHz: row.SampleRateHz.Int64, BitDepth: row.BitDepth.Int64, BitrateKbps: nullInt64Ptr(row.BitrateKbps),
		RecommendedTarget: row.RecommendedTarget, Builtin: row.Builtin == 1,
	}
}

func convertTargetProfiles(rows []settingsdb.TargetProfile) []TargetProfile {
	profiles := make([]TargetProfile, len(rows))
	for i, row := range rows {
		profiles[i] = targetProfileFromRow(row)
	}
	return profiles
}

// Applies says the profile is meant for a media type.
func (p TargetProfile) Applies(mediaType string) bool {
	return slices.Contains(p.MediaTypes, mediaType)
}

// TargetProfiles returns every content profile, by name.
func (s *Snapshot) TargetProfiles() []TargetProfile { return s.targetProfiles }

// TargetProfile looks up a content profile by id.
func (s *Snapshot) TargetProfile(id int64) (TargetProfile, bool) {
	for _, profile := range s.targetProfiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return TargetProfile{}, false
}

func validateTargetProfile(profile TargetProfile, others []TargetProfile) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(profile.Name) == "" {
		problems = append(problems, config.Problem{Path: "profile.name", Expectation: "a name"})
	}
	for _, other := range others {
		if other.ID != profile.ID && strings.EqualFold(other.Name, profile.Name) {
			problems = append(problems, config.Problem{Path: "profile.name", Value: profile.Name, Expectation: "a name no other content profile has"})
		}
	}
	if len(profile.MediaTypes) == 0 {
		problems = append(problems, config.Problem{Path: "profile.media_types", Expectation: "at least one media type"})
	}
	for _, mediaType := range profile.MediaTypes {
		if !slices.Contains(MediaTypes, mediaType) {
			problems = append(problems, config.Problem{Path: "profile.media_types", Value: mediaType, Expectation: "one of " + strings.Join(MediaTypes, ", ")})
		}
	}
	if profile.CRF < 0 || profile.CRF > 51 {
		problems = append(problems, config.Problem{Path: "profile.crf", Expectation: "a CRF from 0 to 51, or none"})
	}
	if profile.FrameRate < 0 || profile.FrameRate > 240 || profile.MaxFrameRate < 0 || profile.MaxFrameRate > 240 {
		problems = append(problems, config.Problem{Path: "profile.frame_rate", Expectation: "frames a second up to 240, or none"})
	}
	return problems
}

func (p TargetProfile) createParams() settingsdb.CreateTargetProfileParams {
	return settingsdb.CreateTargetProfileParams{
		Name: p.Name, MediaTypes: joinList(p.MediaTypes), Description: p.Description,
		Resolution: p.Resolution, VideoCodec: p.VideoCodec, FrameRate: floatToNull(p.FrameRate), MaxFrameRate: floatToNull(p.MaxFrameRate), Crf: floatToNull(p.CRF),
		EncoderPreset: p.EncoderPreset, EncoderTune: p.EncoderTune, Deinterlace: boolToInt(p.Deinterlace),
		AudioCodec: p.AudioCodec, AudioChannels: p.AudioChannels, AudioBitrateKbps: int64PtrToNull(p.AudioBitrateKbps),
		SampleRateHz: positiveToNull(p.SampleRateHz), BitDepth: positiveToNull(p.BitDepth), BitrateKbps: int64PtrToNull(p.BitrateKbps),
		RecommendedTarget: p.RecommendedTarget,
	}
}

func (p TargetProfile) updateParams() settingsdb.UpdateTargetProfileParams {
	created := p.createParams()
	return settingsdb.UpdateTargetProfileParams{
		ID: p.ID, Name: created.Name, MediaTypes: created.MediaTypes, Description: created.Description, Resolution: created.Resolution,
		VideoCodec: created.VideoCodec, FrameRate: created.FrameRate, MaxFrameRate: created.MaxFrameRate, Crf: created.Crf,
		EncoderPreset: created.EncoderPreset, EncoderTune: created.EncoderTune, Deinterlace: created.Deinterlace, AudioCodec: created.AudioCodec,
		AudioChannels: created.AudioChannels, AudioBitrateKbps: created.AudioBitrateKbps, SampleRateHz: created.SampleRateHz,
		BitDepth: created.BitDepth, BitrateKbps: created.BitrateKbps, RecommendedTarget: created.RecommendedTarget,
	}
}

// CreateTargetProfile adds a content profile; every problem is reported at once.
func (c *Cache) CreateTargetProfile(ctx context.Context, profile TargetProfile) (TargetProfile, error) {
	if problems := validateTargetProfile(profile, c.Snapshot().TargetProfiles()); len(problems) > 0 {
		return TargetProfile{}, problems
	}
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		id, err := queries.CreateTargetProfile(ctx, profile.createParams())
		profile.ID = id
		return err
	})
	if err != nil {
		return TargetProfile{}, fmt.Errorf("settings: create target profile %q: %w", profile.Name, err)
	}
	return profile, nil
}

// UpdateTargetProfile replaces a content profile; the targets tuned for it keep their own settings.
func (c *Cache) UpdateTargetProfile(ctx context.Context, profile TargetProfile) error {
	if _, found := c.Snapshot().TargetProfile(profile.ID); !found {
		return fmt.Errorf("%w: target profile %d", ErrNotFound, profile.ID)
	}
	if problems := validateTargetProfile(profile, c.Snapshot().TargetProfiles()); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateTargetProfile(ctx, profile.updateParams())
	})
}

// DeleteTargetProfile removes a content profile; the targets tuned for it lose the link and keep their settings.
func (c *Cache) DeleteTargetProfile(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().TargetProfile(id); !found {
		return fmt.Errorf("%w: target profile %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteTargetProfile(ctx, id)
	})
}
