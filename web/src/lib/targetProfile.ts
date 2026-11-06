import type { components } from "@/api/schema";

type QualityTarget = components["schemas"]["QualityTarget"];
type TargetProfile = components["schemas"]["TargetProfile"];

/** The target a content profile recommends for a media type: the shipped one that fits it and is nearest the preferred settings. */
export function recommendedTarget(profile: TargetProfile, mediaType: string, targets: readonly QualityTarget[]): QualityTarget | undefined {
  return targets.find((target) => target.media_type === mediaType && target.name === profile.recommended_target);
}

/** A target made from a content profile: the recommended target's settings, then whatever the profile itself suggests on top. */
export function applyProfile(target: QualityTarget, profile: TargetProfile, targets: readonly QualityTarget[]): QualityTarget {
  const base = recommendedTarget(profile, target.media_type ?? "", targets);
  const settings: QualityTarget = { ...(base ?? target) };
  for (const own of ["id", "name", "builtin", "label"] as const) delete settings[own];
  const suggested: Partial<QualityTarget> = {};
  if (profile.resolution) suggested.resolution = profile.resolution;
  if (profile.video_codec) suggested.video_codec = profile.video_codec;
  if (profile.frame_rate) suggested.frame_rate = profile.frame_rate;
  if (profile.max_frame_rate) suggested.max_frame_rate = profile.max_frame_rate;
  if (profile.crf) suggested.crf = profile.crf;
  if (profile.encoder_preset) suggested.encoder_preset = profile.encoder_preset;
  if (profile.audio_codec) suggested.audio_codec = profile.audio_codec;
  if (profile.audio_channels) suggested.audio_channels = profile.audio_channels;
  if (profile.audio_bitrate_kbps) suggested.audio_bitrate_kbps = profile.audio_bitrate_kbps;
  if (profile.sample_rate_hz) suggested.sample_rate_hz = profile.sample_rate_hz;
  if (profile.bit_depth) suggested.bit_depth = profile.bit_depth;
  return {
    ...target,
    ...settings,
    ...suggested,
    encoder_tune: profile.encoder_tune ?? "",
    deinterlace: profile.deinterlace ?? false,
    profile_id: profile.id,
    description: target.description || profile.description || "",
  };
}
