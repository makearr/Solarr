import { useEffect, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, X } from "lucide-react";

import { api, ok } from "@/api/client";
import { parseAudioRules } from "@/lib/qualityRules";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaType } from "@/lib/useMediaTypes";
import { isTierVisible, useTier } from "@/lib/useTier";

import { summariseTarget } from "./QualityTargets";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

/** Says a setting follows its media type's default, or offers to return an own choice to it (the choice cleared). */
function InheritNote({ inherited, onReset }: { inherited: boolean; onReset: () => void }) {
  if (inherited) return <p className="text-xs text-accent">This is the default of the library type (Settings, Quality, Defaults by library type).</p>;
  return (
    <button type="button" onClick={onReset} className="self-start text-xs text-ink-muted underline hover:text-ink">
      Use the library type&apos;s default, then Save
    </button>
  );
}

/** A library's own quality: which shared profiles it accepts, in order, where upgrades stop, and
 *  the one quality it aims for. The profiles themselves are made under Quality profiles. */
export function QualityProfiles() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <div className="flex items-baseline gap-3">
        <h1 className="text-lg font-semibold">Quality</h1>
        <Link to="/quality-profiles" className="ml-auto text-xs text-ink-muted hover:text-ink hover:underline">
          Create and edit targets
        </Link>
      </div>
      <LibraryQualityEditor />
    </div>
  );
}

type LibraryQuality = components["schemas"]["LibraryQuality"];
type QualityTarget = components["schemas"]["QualityTarget"];

// Codec to its usual file type, for a target prefilled from a profile.
const CONTAINER_OF: Record<string, string> = { aac: "m4a", alac: "m4a", mp3: "mp3", opus: "opus", vorbis: "ogg", flac: "flac" };

/** "One exact quality" starts at the first profile's own bitrate target (answered 2026-09-30):
 *  AAC around 160 kbps becomes aac, .m4a, 160 kbps. FLAC when the profile names no bitrate. */
export function targetFromProfile(rulesJSON: string | undefined): QualityTarget {
  const lossless: QualityTarget = { audio_codec: "flac", container: "flac", bitrate_kbps: undefined, tolerance_pct: 10 };
  if (!rulesJSON) return lossless;
  let first: { codec: string; ideal: string } | undefined;
  try {
    first = parseAudioRules(rulesJSON).bitrates.find((target) => target.codec && Number(target.ideal) > 0);
  } catch {
    return lossless;
  }
  if (!first) return lossless;
  return {
    audio_codec: first.codec,
    container: CONTAINER_OF[first.codec] ?? first.codec,
    bitrate_kbps: Number(first.ideal),
    tolerance_pct: 10,
  };
}

/** A video library's target starts at the first profile's best tier: its top resolution and codec. */
export function videoTargetFromProfile(rulesJSON: string | undefined): QualityTarget {
  const target: QualityTarget = { audio_codec: "", container: "", tolerance_pct: 0, resolution: "1080p", video_codec: "h265", hdr: "" };
  if (!rulesJSON) return target;
  try {
    const tiers = (JSON.parse(rulesJSON) as { tiers?: { resolution?: string[][]; video_codec?: string[][] } }).tiers;
    target.resolution = tiers?.resolution?.[0]?.[0] ?? target.resolution;
    target.video_codec = tiers?.video_codec?.[0]?.[0] ?? target.video_codec;
  } catch {
    // An unreadable profile leaves the plain default.
  }
  return target;
}

/** A library's quality (2026-09-19, answered 2026-09-24): the profiles it accepts in order of
 *  preference, the cutoff where upgrades stop, and the one target it aims for. */
/** What saving would do to the library: how many items turn upgradable (and so are searched for) or complete. */
function ChangePreview({ id, body, unit }: { id: number; body: components["schemas"]["LibraryQualityWrite"]; unit: string }) {
  const preview = useQuery({
    queryKey: ["libraryQuality", id, "preview", body],
    queryFn: () => ok(api.POST("/categories/{id}/quality/preview", { params: { path: { id } }, body })),
    staleTime: 30_000,
  });
  const data = preview.data;
  if (preview.isLoading) return <p className="text-xs text-ink-muted">Counting what this would change…</p>;
  if (!data) return null;
  if (!data.available) return <p className="text-xs text-ink-muted">No preview: {data.reason ?? "it cannot be counted"}.</p>;
  const plural = (count: number) => `${count} ${unit}${count === 1 ? "" : "s"}`;
  if (data.become_upgradable === 0 && data.stop_being_upgradable === 0) {
    return <p className="text-xs text-ink-muted">Of {plural(data.items)} with files, none would change: {data.upgradable_now} stay upgradable.</p>;
  }
  return (
    <p className="rounded-md border border-border-subtle bg-canvas p-2 text-xs">
      Of {plural(data.items)} with files, {data.become_upgradable} would become upgradable and be searched for a better file,{" "}
      {data.stop_being_upgradable} would count as complete. Upgradable: {data.upgradable_now} now, {data.upgradable_after} after saving.
    </p>
  );
}

function LibraryQualityEditor() {
  const tier = useTier();
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId === undefined ? undefined : Number(categoryId);
  const queryClient = useQueryClient();

  const quality = useQuery({
    queryKey: ["libraryQuality", id],
    enabled: id !== undefined,
    queryFn: () => ok(api.GET("/categories/{id}/quality", { params: { path: { id: id! } } })),
  });

  const [order, setOrder] = useState<number[] | undefined>();
  const [cutoff, setCutoff] = useState<number | undefined>();
  const [upgrades, setUpgrades] = useState(true);
  const [preferred, setPreferred] = useState<"" | "usenet" | "torrent">("");
  const [delayMinutes, setDelayMinutes] = useState(0);
  const [targetId, setTargetId] = useState<number | undefined>();
  const [targetAuto, setTargetAuto] = useState(false);
  const [languageId, setLanguageId] = useState(0);
  // What a search looks for: a season where Wanted lists parts, otherwise the work itself.
  const mediaType = useMediaType(useLibrary(categoryId)?.media_type);
  const unit = ((mediaType?.wanted_grain === "part" ? mediaType.group.one : mediaType?.work.one) ?? "work").toLowerCase();
  const aUnit = `${/^[aeiou]/.test(unit) ? "an" : "a"} ${unit}`;
  useEffect(() => {
    if (!quality.data) return;
    setOrder(quality.data.profiles.map((profile) => profile.id));
    setCutoff(quality.data.cutoff_profile_id ?? undefined);
    setTargetId(quality.data.target_auto ? undefined : (quality.data.target?.id ?? undefined));
    setTargetAuto(quality.data.target_auto ?? false);
    setLanguageId(quality.data.language_profile_id ?? 0);
    setUpgrades(quality.data.upgrades_allowed);
    setPreferred(quality.data.preferred_protocol);
    setDelayMinutes(quality.data.protocol_delay_minutes);
  }, [quality.data]);

  const save = useMutation({
    mutationFn: () => {
      const stored = quality.data!;
      // A value still the media type's default is not sent, so saving something else does not make it the library's own.
      const profilesUntouched = stored.profiles_inherited === true && JSON.stringify(order) === JSON.stringify(stored.profiles.map((profile) => profile.id));
      const targetUntouched = stored.target_inherited === true && targetAuto === (stored.target_auto ?? false) && (targetAuto || targetId === stored.target?.id);
      const languageUntouched = stored.language_inherited === true && languageId === (stored.language_profile_id ?? 0);
      const profileIds = profilesUntouched ? [] : (order ?? []);
      return ok(api.PUT("/categories/{id}/quality", {
        params: { path: { id: id! } },
        body: {
          profile_ids: profileIds,
          cutoff_profile_id: profileIds.length > 0 ? cutoff : undefined,
          target_id: targetUntouched || targetAuto ? undefined : targetId,
          target_auto: targetUntouched ? false : targetAuto,
          language_profile_id: languageUntouched ? 0 : languageId,
          upgrades_allowed: upgrades,
          preferred_protocol: preferred,
          protocol_delay_minutes: delayMinutes,
        },
      }));
    },
    onSuccess: (data: LibraryQuality) => {
      queryClient.setQueryData(["libraryQuality", id], data);
      void queryClient.invalidateQueries({ queryKey: ["category", categoryId] });
    },
  });

  if (id === undefined || !quality.data || order === undefined) return null;
  const names = new Map(quality.data.available.map((profile) => [profile.id, profile.name]));
  const unused = quality.data.available.filter((profile) => !order.includes(profile.id));
  const dirty =
    JSON.stringify([order, cutoff ?? null, targetId ?? null, targetAuto, languageId, upgrades, preferred, delayMinutes]) !==
    JSON.stringify([
      quality.data.profiles.map((profile) => profile.id),
      quality.data.cutoff_profile_id ?? null,
      quality.data.target_auto ? null : (quality.data.target?.id ?? null),
      quality.data.target_auto ?? false,
      quality.data.language_profile_id ?? 0,
      quality.data.upgrades_allowed,
      quality.data.preferred_protocol,
      quality.data.protocol_delay_minutes,
    ]);

  const move = (index: number, by: number) => {
    const next = [...order];
    const [item] = next.splice(index, 1);
    next.splice(index + by, 0, item!);
    setOrder(next);
  };
  const removeAt = (index: number) => {
    const removed = order[index];
    setOrder(order.filter((_, i) => i !== index));
    if (removed === cutoff) setCutoff(undefined);
  };

  return (
    <div className="flex flex-col gap-4">
      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Target quality: the goal</h2>
        <p className="text-xs text-ink-muted">
          What you want this library to hold. Pick one and that is enough: a target with no quality profile ranks every
          release by how near it is, and Solarr aims a little over rather than under. Automatic works the target out from
          the first quality profile.{" "}
          <Link to="/quality-profiles" className="underline hover:text-ink">
            Create and edit targets
          </Link>
        </p>
        <select
          value={targetAuto ? "auto" : (targetId ?? "")}
          onChange={(event) => {
            setTargetAuto(event.target.value === "auto");
            setTargetId(event.target.value === "" || event.target.value === "auto" ? undefined : Number(event.target.value));
          }}
          className={`${inputStyle} max-w-full self-start`}
        >
          <option value="">Nothing in particular: the first profile decides</option>
          <option value="auto" disabled={order.length === 0}>
            Automatic: the average of the first profile{order.length === 0 ? " (needs a profile)" : ""}
          </option>
          {(quality.data.available_targets ?? []).map((target) => (
            <option key={target.id} value={target.id}>
              {target.name}
            </option>
          ))}
        </select>
        {quality.data.target && (targetAuto ? quality.data.target_auto : targetId === quality.data.target.id) && (
          <p className="text-xs text-ink-muted">{summariseTarget(quality.data.target)}</p>
        )}
        <InheritNote
          inherited={quality.data.target_inherited === true}
          onReset={() => {
            setTargetAuto(false);
            setTargetId(undefined);
          }}
        />
      </section>

      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Language</h2>
        <p className="text-xs text-ink-muted">
          Which audio and subtitle languages this library prefers. Like the target, it needs no quality profile; new libraries start with the
          languages chosen in setup.
        </p>
        <select
          value={languageId || ""}
          onChange={(event) => setLanguageId(Number(event.target.value) || 0)}
          className={`${inputStyle} max-w-full self-start`}
        >
          <option value="">No preference</option>
          {(quality.data.available_languages ?? []).map((language) => (
            <option key={language.id} value={language.id}>
              {language.name}
            </option>
          ))}
        </select>
        <InheritNote inherited={quality.data.language_inherited === true} onReset={() => setLanguageId(0)} />
      </section>

      {isTierVisible(tier, "expert") && (
      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Quality profiles (expert)</h2>
        <p className="text-xs text-ink-muted">
          Optional, for experts and anyone coming from the *Arrs. With none the target alone decides. A release the first
          profile wants is preferred; later ones are fallbacks when nothing better is found; a profile not listed is never taken.
        </p>
        {order.length === 0 ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-ink-muted">None chosen: the target above ranks what is found on its own; with no target either, the instance default profile does.</span>
            <button
              type="button"
              onClick={() => setOrder(quality.data.available.map((profile) => profile.id))}
              className="rounded-md border border-border-subtle px-2 py-1 text-xs hover:border-accent"
            >
              Use the suggested order
            </button>
          </div>
        ) : (
          <ol className="flex flex-col divide-y divide-border-subtle rounded-md border border-border-subtle">
            {order.map((profileId, index) => (
              <li key={profileId} className="flex items-center gap-2 px-3 py-2 text-sm">
                <span className="w-5 text-right font-mono text-xs text-ink-muted">{index + 1}</span>
                <span className="min-w-0 flex-1 truncate">{names.get(profileId) ?? `Profile ${profileId}`}</span>
                {cutoff === profileId && (
                  <span className="rounded-full bg-accent/15 px-2 py-px text-[0.6875rem] text-accent">cutoff</span>
                )}
                <button
                  type="button"
                  aria-label={`Move ${names.get(profileId)} up`}
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                  className="rounded p-1 text-ink-muted hover:text-ink disabled:opacity-30"
                >
                  <ArrowUp aria-hidden className="size-3.5" />
                </button>
                <button
                  type="button"
                  aria-label={`Move ${names.get(profileId)} down`}
                  disabled={index === order.length - 1}
                  onClick={() => move(index, 1)}
                  className="rounded p-1 text-ink-muted hover:text-ink disabled:opacity-30"
                >
                  <ArrowDown aria-hidden className="size-3.5" />
                </button>
                <button
                  type="button"
                  aria-label={`Remove ${names.get(profileId)} from this library`}
                  onClick={() => removeAt(index)}
                  className="rounded p-1 text-ink-muted hover:text-danger"
                >
                  <X aria-hidden className="size-3.5" />
                </button>
              </li>
            ))}
          </ol>
        )}
        <InheritNote inherited={quality.data.profiles_inherited === true} onReset={() => setOrder([])} />
        {(quality.data.suggested_profiles ?? []).filter((profile) => !order.includes(profile.id)).length > 0 && (
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-ink-muted">Suggested for this kind of library:</span>
            {(quality.data.suggested_profiles ?? [])
              .filter((profile) => !order.includes(profile.id))
              .map((profile) => (
                <button key={profile.id} type="button" onClick={() => setOrder([...order, profile.id])} className="rounded-md border border-border-subtle px-2 py-1 hover:border-accent">
                  Add {profile.name}
                </button>
              ))}
          </div>
        )}
        {unused.length > 0 && (
          <select
            value=""
            onChange={(event) => setOrder([...order, Number(event.target.value)])}
            className={`${inputStyle} max-w-full self-start`}
          >
            <option value="">+ Add a profile</option>
            {unused.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
          </select>
        )}
      </section>
      )}

      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Upgrades</h2>
        <label className="flex items-start gap-2 text-xs">
          <input type="checkbox" className="mt-0.5" checked={upgrades} onChange={(event) => setUpgrades(event.target.checked)} />
          <span className="flex flex-col">
            <span className="text-ink">Upgrades allowed</span>
            <span className="text-ink-muted">
              Off, {aUnit} with all its files is complete whatever its quality: nothing is searched for an upgrade.
            </span>
          </span>
        </label>
        <p className="text-xs text-ink-muted">
          Once a file is at least this good, or meets the target, it is not upgraded any further.
        </p>
        <select
          disabled={!upgrades}
          value={cutoff ?? ""}
          onChange={(event) => setCutoff(event.target.value === "" ? undefined : Number(event.target.value))}
          className={`${inputStyle} max-w-full self-start`}
        >
          <option value="">No cutoff: always upgrade to the first profile</option>
          {order.map((profileId) => (
            <option key={profileId} value={profileId}>
              {names.get(profileId)}
            </option>
          ))}
        </select>
      </section>

      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Delay profile</h2>
        <p className="text-xs text-ink-muted">
          Prefer one kind of download and give it a head start: a release of the other kind is held back until it has been
          posted this long, then taken like any other. Nothing is refused, so {aUnit} only one kind has is still found.
        </p>
        <div className="flex flex-wrap items-end gap-3 text-xs text-ink-muted">
          <label className="flex flex-col gap-1">
            Prefer
            <select
              value={preferred}
              onChange={(event) => setPreferred(event.target.value as "" | "usenet" | "torrent")}
              className={inputStyle}
            >
              <option value="">No preference</option>
              <option value="usenet">Usenet</option>
              <option value="torrent">Torrents</option>
            </select>
          </label>
          <label className="flex flex-col gap-1">
            Wait for the other (minutes)
            <input
              type="number"
              min={0}
              max={10080}
              disabled={preferred === ""}
              value={delayMinutes}
              onChange={(event) => setDelayMinutes(Math.max(0, Number(event.target.value) || 0))}
              className={`${inputStyle} w-28`}
            />
          </label>
        </div>
      </section>


      {quality.data.warnings.map((warning) => (
        <p key={warning} className="rounded-md border border-warn/50 bg-warn/10 px-3 py-2 text-xs">
          {warning}{" "}
          <Link to="/quality-profiles" className="underline hover:text-ink">
            Edit quality profiles
          </Link>
        </p>
      ))}
      {isTierVisible(tier, "advanced") && (quality.data.suggested_targets ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-ink-muted">Targets this library's first profile accepts:</span>
          {(quality.data.suggested_targets ?? []).map((target) => (
            <button key={target.id} type="button" onClick={() => setTargetId(target.id)} className="rounded-md border border-border-subtle px-2 py-1 hover:border-accent">
              {target.name}
            </button>
          ))}
        </div>
      )}

      {dirty && (
        <ChangePreview
          id={id}
          unit={unit}
          body={{
            profile_ids: order,
            cutoff_profile_id: order.length > 0 ? cutoff : undefined,
            target_id: targetAuto ? undefined : targetId,
            target_auto: targetAuto,
            upgrades_allowed: upgrades,
          }}
        />
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate()}
        >
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {dirty && (
          <button
            type="button"
            onClick={() => {
              setOrder(quality.data.profiles.map((profile) => profile.id));
              setCutoff(quality.data.cutoff_profile_id ?? undefined);
              setTargetId(quality.data.target_auto ? undefined : (quality.data.target?.id ?? undefined));
              setTargetAuto(quality.data.target_auto ?? false);
              setLanguageId(quality.data.language_profile_id ?? 0);
              setUpgrades(quality.data.upgrades_allowed);
              setPreferred(quality.data.preferred_protocol);
              setDelayMinutes(quality.data.protocol_delay_minutes);
            }}
            className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            Discard changes
          </button>
        )}
        {save.isError && (
          <p className="whitespace-pre-wrap text-xs text-danger">
            {(save.error as { detail?: string }).detail ?? "The quality could not be saved."}
          </p>
        )}
      </div>
    </div>
  );
}
