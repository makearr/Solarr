import { useEffect, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, X } from "lucide-react";

import { api } from "@/api/client";
import { parseAudioRules } from "@/lib/qualityRules";
import type { components } from "@/api/schema";

/** A library's own quality: which shared profiles it accepts, in order, where upgrades stop, and
 *  the one quality it aims for. The profiles themselves are made under Quality profiles. */
export function QualityProfiles() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <div className="flex items-baseline gap-3">
        <h1 className="text-lg font-semibold">Quality</h1>
        <Link to="/quality-profiles" className="ml-auto text-xs text-ink-muted hover:text-ink hover:underline">
          Create and edit profiles
        </Link>
      </div>
      <LibraryQualityEditor />
    </div>
  );
}

type LibraryQuality = components["schemas"]["LibraryQuality"];
type QualityTarget = components["schemas"]["QualityTarget"];

// ffprobe's own codec names, which is what the probe measures files by.
const CODECS = ["aac", "mp3", "opus", "vorbis", "flac", "alac"] as const;
const LOSSLESS = new Set(["flac", "alac"]);
const CONTAINERS = ["m4a", "mp3", "opus", "ogg", "flac"] as const;

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

/** A library's quality (2026-09-19, answered 2026-09-24): the profiles it accepts in order of
 *  preference, the cutoff where upgrades stop, and the one target it aims for. */
function LibraryQualityEditor() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId === undefined ? undefined : Number(categoryId);
  const queryClient = useQueryClient();

  const quality = useQuery({
    queryKey: ["libraryQuality", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/quality", { params: { path: { id: id! } } });
      if (error) throw error;
      return data;
    },
  });

  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });

  const [order, setOrder] = useState<number[] | undefined>();
  const [cutoff, setCutoff] = useState<number | undefined>();
  const [upgrades, setUpgrades] = useState(true);
  const [target, setTarget] = useState<QualityTarget | undefined>();
  useEffect(() => {
    if (!quality.data) return;
    setOrder(quality.data.profiles.map((profile) => profile.id));
    setCutoff(quality.data.cutoff_profile_id ?? undefined);
    setTarget(quality.data.target ?? undefined);
    setUpgrades(quality.data.upgrades_allowed);
  }, [quality.data]);

  const save = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.PUT("/categories/{id}/quality", {
        params: { path: { id: id! } },
        body: { profile_ids: order ?? [], cutoff_profile_id: cutoff, target, upgrades_allowed: upgrades },
      });
      if (error) throw error;
      return data;
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
    JSON.stringify([order, cutoff ?? null, target ?? null, upgrades]) !==
    JSON.stringify([
      quality.data.profiles.map((profile) => profile.id),
      quality.data.cutoff_profile_id ?? null,
      quality.data.target ?? null,
      quality.data.upgrades_allowed,
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
        <h2 className="text-sm font-medium">Profiles, in order of preference</h2>
        <p className="text-xs text-ink-muted">
          A release the first profile wants is preferred; later ones are fallbacks when nothing better
          is found. A profile not listed is never taken.
        </p>
        {order.length === 0 ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-ink-muted">None chosen: this library uses the instance default.</span>
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
        {unused.length > 0 && (
          <select
            value=""
            onChange={(event) => setOrder([...order, Number(event.target.value)])}
            className={`${inputStyle} self-start`}
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

      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Upgrades</h2>
        <label className="flex items-start gap-2 text-xs">
          <input type="checkbox" className="mt-0.5" checked={upgrades} onChange={(event) => setUpgrades(event.target.checked)} />
          <span className="flex flex-col">
            <span className="text-ink">Upgrades allowed</span>
            <span className="text-ink-muted">
              Off, an album with every track is complete whatever its quality: nothing is searched for an upgrade.
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
          className={`${inputStyle} self-start`}
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
        <h2 className="text-sm font-medium">Target</h2>
        <p className="text-xs text-ink-muted">
          The quality you would pick if you could choose freely. A file meets it with the same codec and
          file type and a bitrate within the tolerance; a lossless target has no bitrate.
        </p>
        <label className="flex w-fit flex-col gap-1 text-xs text-ink-muted">
          Aim for
          <select
            value={target ? "exact" : "none"}
            onChange={(event) =>
              setTarget(
                event.target.value === "exact"
                  ? targetFromProfile(profiles.data?.find((profile) => profile.id === order?.[0])?.rules)
                  : undefined,
              )
            }
            className={inputStyle}
          >
            <option value="none">Nothing in particular: the first profile decides</option>
            <option value="exact">One exact quality</option>
          </select>
        </label>
        {target && (
          <div className="flex flex-wrap items-end gap-3 text-xs">
            <label className="flex flex-col gap-1 text-ink-muted">
              Codec
              <select
                value={target.audio_codec}
                onChange={(event) => {
                  const codec = event.target.value;
                  setTarget({
                    ...target,
                    audio_codec: codec,
                    bitrate_kbps: LOSSLESS.has(codec) ? undefined : (target.bitrate_kbps ?? 256),
                  });
                }}
                className={inputStyle}
              >
                {CODECS.map((codec) => (
                  <option key={codec} value={codec}>
                    {codec}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-ink-muted">
              File type
              <select
                value={target.container}
                onChange={(event) => setTarget({ ...target, container: event.target.value })}
                className={inputStyle}
              >
                {CONTAINERS.map((container) => (
                  <option key={container} value={container}>
                    .{container}
                  </option>
                ))}
              </select>
            </label>
            {!LOSSLESS.has(target.audio_codec) && (
              <>
                <label className="flex flex-col gap-1 text-ink-muted">
                  Bitrate (kbps)
                  <input
                    type="number"
                    min={32}
                    max={1411}
                    value={target.bitrate_kbps ?? ""}
                    onChange={(event) => setTarget({ ...target, bitrate_kbps: Number(event.target.value) })}
                    className={`${inputStyle} w-24`}
                  />
                </label>
                <label className="flex flex-col gap-1 text-ink-muted">
                  Tolerance (±%)
                  <input
                    type="number"
                    min={0}
                    max={50}
                    value={target.tolerance_pct}
                    onChange={(event) => setTarget({ ...target, tolerance_pct: Number(event.target.value) })}
                    className={`${inputStyle} w-20`}
                  />
                </label>
              </>
            )}
          </div>
        )}
      </section>

      {quality.data.warnings.map((warning) => (
        <p key={warning} className="rounded-md border border-warn/50 bg-warn/10 px-3 py-2 text-xs">
          {warning}{" "}
          <Link to="/quality-profiles" className="underline hover:text-ink">
            Edit quality profiles
          </Link>
        </p>
      ))}

      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate()}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        {dirty && (
          <button
            type="button"
            onClick={() => {
              setOrder(quality.data.profiles.map((profile) => profile.id));
              setCutoff(quality.data.cutoff_profile_id ?? undefined);
              setTarget(quality.data.target ?? undefined);
              setUpgrades(quality.data.upgrades_allowed);
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

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
