import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, tOr } from "@/lib/i18n";
import { familyOf, targetTitle } from "@/lib/targetDisplay";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { Card } from "./SettingsCard";

type Setup = components["schemas"]["TranscodeSetup"];
type State = components["schemas"]["TranscodeSetupState"];
type Availability = components["schemas"]["TranscodeEngineAvailability"];
type Strength = components["schemas"]["TranscodeFilterStrength"];

const PRESETS = ["fast", "normal", "slow", "slower", "veryslow"] as const;
const STRENGTHS = ["off", "low", "medium", "strong"] as const;
const QUALITY_LABEL: Record<string, string> = { crf: "CRF", la_icq: "ICQ (global quality)", icq: "ICQ (global quality)", cqp: "QP", cq: "CQ" };

/** The label of the quality number for the engine the setup picks: software and auto speak CRF. */
export function qualityLabel(engine: string, availability: Availability[]): string {
  const mode = availability.find((one) => one.engine === engine)?.quality_mode ?? "";
  return QUALITY_LABEL[engine === "software" || engine === "auto" || mode === "" ? "crf" : mode] ?? "CRF";
}

function Check({ label, checked, onChange, disabled }: { label: string; checked: boolean; onChange: (value: boolean) => void; disabled?: boolean }) {
  return (
    <label className={`flex items-center gap-2 text-sm ${disabled ? "opacity-50" : ""}`}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  );
}

function Num({ label, value, onChange, min, max, step }: { label: string; value: number; onChange: (value: number) => void; min?: number; max?: number; step?: number }) {
  return (
    <label className={labelStyle}>
      {label}
      <input type="number" className={`${inputStyle} w-28`} value={value} min={min} max={max} step={step} onChange={(event) => onChange(Number(event.target.value) || 0)} />
    </label>
  );
}

function Pick<T extends string>({ label, value, options, onChange, disabled }: { label: string; value: T; options: readonly (readonly [T, string])[]; onChange: (value: T) => void; disabled?: boolean }) {
  return (
    <label className={labelStyle}>
      {label}
      <select className={inputStyle} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value as T)}>
        {options.map(([option, text]) => (
          <option key={option} value={option}>
            {text}
          </option>
        ))}
      </select>
    </label>
  );
}

/** A comma-separated list of codec names, edited as text and committed on leaving the field. */
function CodecList({ label, value, onChange }: { label: string; value: string[]; onChange: (value: string[]) => void }) {
  const [text, setText] = useState(value.join(", "));
  useEffect(() => setText(value.join(", ")), [value]);
  return (
    <label className={labelStyle}>
      {label}
      <input
        className={`${inputStyle} w-48`}
        value={text}
        onChange={(event) => setText(event.target.value)}
        onBlur={() => onChange(text.split(",").map((one) => one.trim().toLowerCase()).filter(Boolean))}
      />
    </label>
  );
}

const strengthOptions = STRENGTHS.map((strength) => [strength, t(`transcode.setup.strength.${strength}`)] as const);

/** The red block above the form, and on the Transcode page: how this setup is likely to miss its target. */
export function SetupWarnings({ warnings }: { warnings: components["schemas"]["TranscodeWarning"][] }) {
  if (warnings.length === 0) return null;
  return (
    <div role="alert" className="flex flex-col gap-1 rounded-lg border-2 border-danger bg-danger/10 p-3 text-sm text-danger">
      {warnings.map((warning) => (
        <p key={warning.code}>{tOr(`transcode.warning.${warning.code}`, warning.code).replace("{value}", warning.value ?? "")}</p>
      ))}
    </div>
  );
}

/** Settings > Transcode > one target: how files are converted to it. */
export function TranscodeSetupForm() {
  const { targetId } = useParams({ strict: false }) as { targetId: string };
  const id = Number(targetId);
  const queryClient = useQueryClient();
  const expert = isTierVisible(useTier(), "expert");
  const state = useQuery({ queryKey: ["transcode-setup", id], queryFn: () => ok(api.GET("/settings/transcode/targets/{target_id}", { params: { path: { target_id: id } } })) });
  const targets = useQuery({ queryKey: ["qualityTargets"], queryFn: () => ok(api.GET("/qualitytargets")) });
  const [draft, setDraft] = useState<Setup | null>(null);
  useEffect(() => {
    if (state.data) setDraft(state.data.setup);
  }, [state.data]);
  const receive = (data: State) => {
    queryClient.setQueryData(["transcode-setup", id], data);
    void queryClient.invalidateQueries({ queryKey: ["transcode-summary"] });
  };
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (body: Setup) => ok(api.PUT("/settings/transcode/targets/{target_id}", { params: { path: { target_id: id } }, body })),
    onSuccess: receive,
  });
  const reset = useMutation({
    mutationFn: () => ok(api.DELETE("/settings/transcode/targets/{target_id}", { params: { path: { target_id: id } } })),
    onSuccess: receive,
  });
  const [copyFrom, setCopyFrom] = useState("");
  const copy = useMutation({
    mutationFn: (source: number) => ok(api.GET("/settings/transcode/targets/{target_id}", { params: { path: { target_id: source } } })),
    onSuccess: (data) => setDraft(data.setup),
  });
  const target = targets.data?.find((one) => one.id === id);
  if (!state.data || !draft) return null;
  const availability = state.data.availability;
  const filters = state.data.filters;
  const isAudio = !!target && ["music", "spoken"].includes(familyOf(target.media_type ?? ""));
  const patch = <K extends Exclude<keyof Setup, "version">>(section: K, change: Partial<Setup[K]>) => setDraft({ ...draft, [section]: { ...(draft[section] as object), ...change } });
  const { encode, skip, streams, video_filters: video, audio_filters: audio, check, ladder } = draft;
  const siblings = (targets.data ?? []).filter((one) => one.id !== id && target && familyOf(one.media_type ?? "") === familyOf(target.media_type ?? ""));
  const bitrateMode = encode.rate_mode === "bitrate";
  const chosen = availability.find((one) => one.engine === encode.engine);
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-baseline gap-3">
        <Link to="/settings/transcode" className="text-xs text-ink-muted hover:text-ink">
          {t("transcode.setup.back")}
        </Link>
        <h2 className="text-sm font-medium">{target ? targetTitle(target) : `#${id}`}</h2>
        <span className="text-xs text-ink-muted">
          {[target?.container, target?.video_codec, target?.resolution, target?.audio_codec].filter(Boolean).join(" · ")}
        </span>
        <span className="text-xs text-ink-muted">{state.data.is_default ? t("transcode.setup.default") : t("transcode.setup.customised")}</span>
      </div>
      <SetupWarnings warnings={state.data.warnings} />

      <Card title={t("transcode.setup.encoder")} help={t("transcode.setup.encoderHelp")}>
        <fieldset className="flex flex-col gap-1">
          <legend className="mb-1 text-xs text-ink-muted">{t("transcode.engine")}</legend>
          {availability.map((one) => (
            <label key={one.engine} className={`flex flex-wrap items-center gap-2 text-sm ${one.available ? "" : "opacity-60"}`}>
              <input type="radio" name="engine" value={one.engine} checked={encode.engine === one.engine} disabled={!one.available} onChange={() => patch("encode", { engine: one.engine })} />
              {t(`transcode.setup.engine.${one.engine}`)}
              {!one.available && <span className="text-xs text-ink-muted">{one.reason ?? t("transcode.unavailable")}</span>}
            </label>
          ))}
        </fieldset>
        <Check label={t("transcode.setup.cpuFallback")} checked={encode.allow_cpu_fallback} onChange={(allow_cpu_fallback) => patch("encode", { allow_cpu_fallback })} />
        {!isAudio && (
          <div className="flex flex-wrap items-end gap-4">
            <Pick
              label={t("transcode.setup.rateMode")}
              value={encode.rate_mode === "bitrate" ? "bitrate" : "quality"}
              options={[["quality", t("transcode.setup.rateQuality")], ["bitrate", t("transcode.setup.rateBitrate")]] as const}
              onChange={(rate_mode) => patch("encode", { rate_mode })}
            />
            {bitrateMode ? (
              <Num label={t("transcode.setup.bitrate")} value={encode.video_bitrate_kbps} min={0} onChange={(video_bitrate_kbps) => patch("encode", { video_bitrate_kbps })} />
            ) : (
              <Num label={qualityLabel(encode.engine, availability)} value={encode.quality} min={0} max={63} step={0.5} onChange={(quality) => patch("encode", { quality })} />
            )}
            <Pick
              label={t("transcode.setup.preset")}
              value={encode.preset === "" ? "normal" : encode.preset}
              options={PRESETS.map((preset) => [preset, preset] as const)}
              disabled={chosen ? !chosen.preset_applies : false}
              onChange={(preset) => patch("encode", { preset })}
            />
            {expert && <Num label={t("transcode.setup.offset")} value={encode.hardware_quality_offset} step={0.5} onChange={(hardware_quality_offset) => patch("encode", { hardware_quality_offset })} />}
          </div>
        )}
        {chosen && !chosen.preset_applies && <p className="text-xs text-ink-muted">{t("transcode.setup.noPreset")}</p>}
        {chosen && chosen.available && !chosen.lookahead && encode.engine !== "auto" && <p className="text-xs text-ink-muted">{t("transcode.setup.noLookahead")}</p>}
      </Card>

      <Card title={t("transcode.setup.skip")} help={t("transcode.setup.skipHelp")}>
        <Check label={t("transcode.setup.skip.transcoded_before")} checked={skip.transcoded_before} onChange={(transcoded_before) => patch("skip", { transcoded_before })} />
        <Check label={t("transcode.setup.skip.container_matches")} checked={skip.container_matches} onChange={(container_matches) => patch("skip", { container_matches })} />
        <Check label={t("transcode.setup.skip.codec_matches")} checked={skip.codec_matches} onChange={(codec_matches) => patch("skip", { codec_matches })} />
        {!isAudio && <Check label={t("transcode.setup.skip.resolution_at_or_below")} checked={skip.resolution_at_or_below} onChange={(resolution_at_or_below) => patch("skip", { resolution_at_or_below })} />}
        <Check label={t("transcode.setup.skip.rate_at_or_below")} checked={skip.rate_at_or_below} onChange={(rate_at_or_below) => patch("skip", { rate_at_or_below })} />
        {!isAudio && <Check label={t("transcode.setup.skip.only_wanted_languages")} checked={skip.only_wanted_languages} onChange={(only_wanted_languages) => patch("skip", { only_wanted_languages })} />}
        <div className="flex flex-wrap items-end gap-4">
          {!isAudio && <CodecList label={t("transcode.setup.skip.alsoVideo")} value={skip.also_video_codecs} onChange={(also_video_codecs) => patch("skip", { also_video_codecs })} />}
          <CodecList label={t("transcode.setup.skip.alsoAudio")} value={skip.also_audio_codecs} onChange={(also_audio_codecs) => patch("skip", { also_audio_codecs })} />
        </div>
        <div className="flex flex-wrap items-end gap-4">
          <Check label={t("transcode.setup.skip.within_range")} checked={skip.within_range} onChange={(within_range) => patch("skip", { within_range })} />
          <Num label="%" value={skip.range_pct} min={1} max={100} onChange={(range_pct) => patch("skip", { range_pct })} />
          <Check label={t("transcode.setup.skip.below_max")} checked={skip.below_max} onChange={(below_max) => patch("skip", { below_max })} />
          <Num label="%" value={skip.max_pct} min={1} max={1000} onChange={(max_pct) => patch("skip", { max_pct })} />
        </div>
      </Card>

      <Card title={t("transcode.setup.streams")} help={t("transcode.setup.streamsHelp")}>
        <div className="flex flex-wrap items-end gap-4">
          {!isAudio && (
            <>
              <Check label={t("transcode.setup.streams.copy_video_on_target")} checked={streams.copy_video_on_target} onChange={(copy_video_on_target) => patch("streams", { copy_video_on_target })} />
              <Num label={t("transcode.setup.streams.copyMaxPct")} value={streams.video_copy_max_pct} min={1} max={1000} onChange={(video_copy_max_pct) => patch("streams", { video_copy_max_pct })} />
            </>
          )}
        </div>
        <div className="flex flex-wrap items-end gap-4">
          <Check label={t("transcode.setup.streams.copy_audio_on_target")} checked={streams.copy_audio_on_target} onChange={(copy_audio_on_target) => patch("streams", { copy_audio_on_target })} />
          <Num label={t("transcode.setup.streams.copyMaxPct")} value={streams.audio_copy_max_pct} min={1} max={1000} onChange={(audio_copy_max_pct) => patch("streams", { audio_copy_max_pct })} />
        </div>
        {!isAudio && (
          <>
            <Check label={t("transcode.setup.streams.downscale")} checked={streams.downscale} onChange={(downscale) => patch("streams", { downscale })} />
            <Check label={t("transcode.setup.streams.no_upscale")} checked={streams.no_upscale} onChange={(no_upscale) => patch("streams", { no_upscale })} />
            <Check label={t("transcode.setup.streams.reduce_fps")} checked={streams.reduce_fps} onChange={(reduce_fps) => patch("streams", { reduce_fps })} />
            <Check label={t("transcode.setup.streams.no_fps_increase")} checked={streams.no_fps_increase} onChange={(no_fps_increase) => patch("streams", { no_fps_increase })} />
            <Check label={t("transcode.setup.streams.remove_unwanted_audio")} checked={streams.remove_unwanted_audio} onChange={(remove_unwanted_audio) => patch("streams", { remove_unwanted_audio })} />
            <Check label={t("transcode.setup.streams.remove_unwanted_subtitles")} checked={streams.remove_unwanted_subtitles} onChange={(remove_unwanted_subtitles) => patch("streams", { remove_unwanted_subtitles })} />
          </>
        )}
        <Check label={t("transcode.setup.streams.downmix")} checked={streams.downmix} onChange={(downmix) => patch("streams", { downmix })} />
        <Check label={t("transcode.setup.streams.no_upmix")} checked={streams.no_upmix} onChange={(no_upmix) => patch("streams", { no_upmix })} />
        <Check label={t("transcode.setup.streams.no_sample_rate_increase")} checked={streams.no_sample_rate_increase} onChange={(no_sample_rate_increase) => patch("streams", { no_sample_rate_increase })} />
      </Card>

      {!isAudio && expert && (
        <Card title={t("transcode.setup.videoFilters")} help={t("transcode.setup.videoFiltersHelp")}>
          <div className="flex flex-wrap items-end gap-4">
            <Check label={t("transcode.setup.vf.deinterlace")} checked={video.deinterlace} onChange={(deinterlace) => patch("video_filters", { deinterlace })} />
            <Check label={t("transcode.setup.vf.tonemap")} checked={video.tonemap} onChange={(tonemap) => patch("video_filters", { tonemap })} />
            {(["denoise", "deblock", "deband", "deflicker", "sharpen"] as const).map((name) => (
              <Pick key={name} label={t(`transcode.setup.vf.${name}`)} value={video[name] === "" ? "off" : video[name]} options={strengthOptions} onChange={(value: Strength) => patch("video_filters", { [name]: value })} />
            ))}
          </div>
          <p className="text-xs text-ink-muted">{t("transcode.setup.cpuDecodeNote")}</p>
          {Object.entries(filters).some(([, has]) => !has) && <p className="text-xs text-ink-muted">{t("transcode.setup.missingFilters", { names: Object.entries(filters).filter(([, has]) => !has).map(([name]) => name).join(", ") })}</p>}
        </Card>
      )}

      {expert && (
        <Card title={t("transcode.setup.audioFilters")} help={t("transcode.setup.audioFiltersHelp")}>
          <div className="flex flex-wrap items-end gap-4">
            <Check label={t("transcode.setup.af.normalize")} checked={audio.normalize} onChange={(normalize) => patch("audio_filters", { normalize })} />
            <Check label={t("transcode.setup.af.dialog_enhance")} checked={audio.dialog_enhance} onChange={(dialog_enhance) => patch("audio_filters", { dialog_enhance })} />
            <Check label={t("transcode.setup.af.compress")} checked={audio.compress} onChange={(compress) => patch("audio_filters", { compress })} />
            <Check label={t("transcode.setup.af.declip")} checked={audio.declip} onChange={(declip) => patch("audio_filters", { declip })} />
            <Pick label={t("transcode.setup.af.denoise")} value={audio.denoise === "" ? "off" : audio.denoise} options={strengthOptions} onChange={(denoise) => patch("audio_filters", { denoise })} />
          </div>
        </Card>
      )}

      <Card title={t("transcode.setup.check")} help={t("transcode.setup.checkHelp")}>
        <div className="flex flex-wrap items-end gap-4">
          <Check label={t("transcode.setup.check.duration")} checked={check.duration} onChange={(duration) => patch("check", { duration })} />
          <Num label={t("transcode.setup.check.seconds")} value={check.duration_seconds} min={0} max={60} onChange={(duration_seconds) => patch("check", { duration_seconds })} />
          <Check label={t("transcode.setup.check.smaller")} checked={check.smaller} onChange={(smaller) => patch("check", { smaller })} />
          <Check label={t("transcode.setup.skip.within_range")} checked={check.within_range} onChange={(within_range) => patch("check", { within_range })} />
          <Num label="%" value={check.range_pct} min={1} max={100} onChange={(range_pct) => patch("check", { range_pct })} />
          <Check label={t("transcode.setup.skip.below_max")} checked={check.below_max} onChange={(below_max) => patch("check", { below_max })} />
          <Num label="%" value={check.max_pct} min={1} max={1000} onChange={(max_pct) => patch("check", { max_pct })} />
        </div>
      </Card>

      <Card title={t("transcode.setup.ladder")} help={t("transcode.setup.ladderHelp")}>
        <div className="flex flex-wrap items-end gap-4">
          <Num label={t("transcode.setup.ladder.passes")} value={ladder.extra_passes} min={0} max={4} onChange={(extra_passes) => patch("ladder", { extra_passes })} />
          <Num label={t("transcode.setup.ladder.qualityStep")} value={ladder.quality_step} min={0.5} step={0.5} onChange={(quality_step) => patch("ladder", { quality_step })} />
          <Num label={t("transcode.setup.ladder.bitrateStep")} value={ladder.bitrate_step_pct} min={1} max={90} onChange={(bitrate_step_pct) => patch("ladder", { bitrate_step_pct })} />
        </div>
      </Card>

      <div className="flex flex-wrap items-end gap-3">
        <Button variant="primary" busy={save.isPending} onClick={() => save.mutate(draft)}>
          {t("transcode.save")}
        </Button>
        <Button busy={reset.isPending} onClick={() => reset.mutate()}>
          {t("transcode.setup.reset")}
        </Button>
        <label className={labelStyle}>
          {t("transcode.setup.copyFrom")}
          <select
            className={inputStyle}
            value={copyFrom}
            onChange={(event) => {
              setCopyFrom(event.target.value);
              if (event.target.value) copy.mutate(Number(event.target.value));
            }}
          >
            <option value="" />
            {siblings.map((one) => (
              <option key={one.id} value={one.id}>
                {targetTitle(one)}
              </option>
            ))}
          </select>
        </label>
      </div>
    </div>
  );
}
