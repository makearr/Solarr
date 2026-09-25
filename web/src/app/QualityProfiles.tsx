import { useEffect, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Trash2, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type QualityProfile = components["schemas"]["QualityProfile"];

/** ADR-40's policy CRUD: internal/identify/score already interprets the
 *  rules/upgrade JSON, and internal/db/settings.Cache already validates and
 *  stores it -- this is the REST API and builder UI LibraryConfigPlaceholder's
 *  own "quality" section had been standing in for (todo 4.3, 2026-09-12).
 *  rules/upgrade stay raw JSON textareas rather than a visual gate/tier
 *  builder: the interpreter's own shape (hard gates, then tiers, then an
 *  additive score, ADR-40) is expressive enough that a form covering it
 *  would just be a worse JSON editor -- not attempted without a real need
 *  for one. Global, not per-library: reached from a library's config
 *  section, same as Packs, but the same list regardless of which category
 *  got you here (a category merely references one by id, not built yet --
 *  desired_item creation for music is itself blocked on this existing). */
export function QualityProfiles() {
  const tier = useTier();
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);

  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["qualityProfiles"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/qualityprofiles/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">Quality</h1>
      <LibraryQualityEditor />
      {!isTierVisible(tier, "expert") ? null : (
        <>
          <h2 className="pt-4 text-sm font-medium">All profiles</h2>
          <p className="text-sm leading-relaxed text-ink-muted">
            Hard gates, then tiers, then an additive score. Editing the rules directly is
            expert work; picking one for a library above is not.
          </p>
        </>
      )}
      {isTierVisible(tier, "expert") && remove.isError && (
        <p className="text-xs text-danger">
          {(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}
        </p>
      )}

      {profiles.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {profiles.error !== null && (
        <p className="text-sm text-danger">
          {(profiles.error as { detail?: string }).detail ?? "Quality profiles could not be read."}
        </p>
      )}

      {isTierVisible(tier, "expert") && profiles.data && profiles.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {profiles.data.map((profile) =>
            editingId === profile.id ? (
              <li key={profile.id} className="p-3">
                <QualityProfileForm
                  initial={profile}
                  onDone={() => {
                    setEditingId(null);
                    invalidate();
                  }}
                  onCancel={() => setEditingId(null)}
                />
              </li>
            ) : (
              <li key={profile.id} className="flex items-center gap-2 px-3 py-2.5">
                <span className="min-w-0 flex-1 truncate text-sm font-medium">{profile.name}</span>
                <button
                  type="button"
                  onClick={() => setEditingId(profile.id)}
                  className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                             hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                             focus-visible:outline-accent"
                >
                  Edit
                </button>
                <button
                  type="button"
                  aria-label={`Remove quality profile ${profile.name}`}
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(profile.id)}
                  className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                             disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                             focus-visible:outline-accent"
                >
                  <Trash2 aria-hidden className="size-3.5" />
                </button>
              </li>
            ),
          )}
        </ul>
      ) : (
        isTierVisible(tier, "expert") &&
        !profiles.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
            No quality profile exists yet.
          </p>
        )
      )}

      {isTierVisible(tier, "expert") &&
        (creating ? (
          <div className="rounded-lg border border-border-subtle bg-surface p-3">
            <QualityProfileForm
              onDone={() => {
                setCreating(false);
                invalidate();
              }}
              onCancel={() => setCreating(false)}
            />
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="w-fit rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                       hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                       focus-visible:outline-accent"
          >
            New quality profile
          </button>
        ))}
    </div>
  );
}

function QualityProfileForm({
  initial,
  onDone,
  onCancel,
}: {
  initial?: QualityProfile;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [rules, setRules] = useState(initial?.rules ?? "{}");
  const [upgrade, setUpgrade] = useState(initial?.upgrade ?? "{}");

  const save = useMutation({
    mutationFn: async () => {
      const body = { name, rules, upgrade };
      const { error } = initial
        ? await api.PATCH("/qualityprofiles/{id}", { params: { path: { id: initial.id } }, body })
        : await api.POST("/qualityprofiles", { body });
      if (error) throw error;
    },
    onSuccess: onDone,
  });

  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          autoComplete="off"
          autoFocus
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Rules (tiers and targets, JSON)
        <textarea
          className={`${inputStyle} h-32 font-mono text-xs`}
          value={rules}
          spellCheck={false}
          onChange={(event) => setRules(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Upgrade policy JSON
        <textarea
          className={`${inputStyle} h-20 font-mono text-xs`}
          value={upgrade}
          spellCheck={false}
          onChange={(event) => setUpgrade(event.target.value)}
        />
      </label>
      {save.isError && (
        <p className="text-xs text-danger">
          {(save.error as { detail?: string }).detail ?? "That profile could not be saved."}
        </p>
      )}
      <div className="flex gap-2">
        <button
          type="submit"
          disabled={!name.trim() || save.isPending}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** Which profile this library uses, as cards. The shipped four are plain
 *  choices a normal user should be able to make; only editing the rules
 *  behind them is expert work (revision 9.2). Opened from a library's own
 *  config section, so there is a category to assign to -- reached any other
 *  way there is nothing to pick for and it stays hidden. */
type LibraryQuality = components["schemas"]["LibraryQuality"];
type QualityTarget = components["schemas"]["QualityTarget"];

// ffprobe's own codec names, which is what the probe measures files by.
const CODECS = ["aac", "mp3", "opus", "vorbis", "flac", "alac"] as const;
const LOSSLESS = new Set(["flac", "alac"]);
const CONTAINERS = ["m4a", "mp3", "opus", "ogg", "flac"] as const;

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

  const [order, setOrder] = useState<number[] | undefined>();
  const [cutoff, setCutoff] = useState<number | undefined>();
  const [target, setTarget] = useState<QualityTarget | undefined>();
  useEffect(() => {
    if (!quality.data) return;
    setOrder(quality.data.profiles.map((profile) => profile.id));
    setCutoff(quality.data.cutoff_profile_id ?? undefined);
    setTarget(quality.data.target ?? undefined);
  }, [quality.data]);

  const save = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.PUT("/categories/{id}/quality", {
        params: { path: { id: id! } },
        body: { profile_ids: order ?? [], cutoff_profile_id: cutoff, target },
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
    JSON.stringify([order, cutoff ?? null, target ?? null]) !==
    JSON.stringify([
      quality.data.profiles.map((profile) => profile.id),
      quality.data.cutoff_profile_id ?? null,
      quality.data.target ?? null,
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
        <h2 className="text-sm font-medium">Cutoff</h2>
        <p className="text-xs text-ink-muted">
          Once a file is at least this good, or meets the target, it is not upgraded any further.
        </p>
        <select
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
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-medium">Target</h2>
          <label className="ml-auto flex items-center gap-1 text-xs text-ink-muted">
            <input
              type="checkbox"
              checked={target !== undefined}
              onChange={(event) =>
                setTarget(
                  event.target.checked
                    ? { audio_codec: "aac", container: "m4a", bitrate_kbps: 256, tolerance_pct: 10 }
                    : undefined,
                )
              }
            />
            Aim for one exact quality
          </label>
        </div>
        <p className="text-xs text-ink-muted">
          The quality you would pick if you could choose freely. A file meets it with the same codec and
          file type and a bitrate within the tolerance; a lossless target has no bitrate.
        </p>
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
          {warning}
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
