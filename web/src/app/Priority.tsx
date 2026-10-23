import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";
import { inputStyle, labelStyle } from "@/ui/form";

import { Card, Field, SaveBar } from "./SettingsCard";

type PrioritySettings = components["schemas"]["PrioritySettings"];
type SearchShare = components["schemas"]["LibrarySearchShare"];
type HuntMode = NonNullable<PrioritySettings["hunt_mode"]>;

// The automatic search's run interval choices; each divides the hour, so the runs are evenly spaced.
const HUNT_MINUTES = [5, 10, 15, 20, 30, 60] as const;
const HUNT_TASK = "hunt";

/** The minutes between the hunt's runs when its cadence is a plain "every N minutes", else undefined. */
function huntMinutes(cadences: readonly string[] | undefined): number | undefined {
  if (cadences?.length !== 1) return undefined;
  if (cadences[0] === "0 * * * *") return 60;
  const every = /^\*\/(\d+) \* \* \* \*$/.exec(cadences[0] ?? "");
  return every ? Number(every[1]) : undefined;
}

function huntCadence(minutes: number): string {
  return minutes === 60 ? "0 * * * *" : `*/${minutes} * * * *`;
}

const HUNT_MODES: readonly (readonly [HuntMode, string])[] = [
  ["oldest_searched", "Oldest searched first"],
  ["newest_searched", "Newest searched first"],
  ["oldest_release", "Oldest release first"],
  ["newest_release", "Newest release first"],
  ["random_oldest_searched", "Random among the oldest searched"],
];

/** ADR-36/ADR-55's trigger weights and freshness window, and the automatic search budget (section 9.4). */
export function Priority() {
  const tier = useTier();
  const queryClient = useQueryClient();
  const priority = useQuery({
    queryKey: ["priority"],
    queryFn: () => ok(api.GET("/priority")),
  });

  const schedules = useQuery({
    queryKey: ["schedules"],
    queryFn: () => ok(api.GET("/schedules")),
  });
  const huntCadences = schedules.data?.find((schedule) => schedule.key === HUNT_TASK)?.cadences;

  const update = useMutation({
    mutationFn: async ({ body, minutes }: { body: PrioritySettings; minutes?: number }) => {
      await ok(api.PATCH("/priority", { body }));
      if (minutes !== undefined && minutes !== huntMinutes(huntCadences)) {
        await ok(api.PATCH("/schedules/{key}", { params: { path: { key: HUNT_TASK } }, body: { cadences: [huntCadence(minutes)] } }));
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["priority"] });
      void queryClient.invalidateQueries({ queryKey: ["schedules"] });
    },
  });

  if (!isTierVisible(tier, "advanced")) return null;
  if (priority.error !== null || !priority.data || !schedules.data) return null;

  return (
    <section className="flex flex-col gap-3">
      <PriorityForm
        key={huntCadences?.join("|")}
        priority={priority.data}
        minutes={huntMinutes(huntCadences)}
        onSave={(body, minutes) => update.mutate({ body, minutes })}
        busy={update.isPending}
      />
      {update.isError && <p className="text-sm text-danger">That change was refused.</p>}
    </section>
  );
}

function PriorityForm({
  priority,
  minutes: storedMinutes,
  onSave,
  busy,
}: {
  priority: PrioritySettings;
  /** Undefined when the hunt's cadence is a custom one, set under Scheduler. */
  minutes: number | undefined;
  onSave: (body: PrioritySettings, minutes: number | undefined) => void;
  busy: boolean;
}) {
  const [minutes, setMinutes] = useState(storedMinutes);
  const [shares, setShares] = useState<SearchShare[]>(priority.search_shares ?? []);
  const [userRequestWeight, setUserRequestWeight] = useState(priority.user_request_weight);
  const [newReleaseWeight, setNewReleaseWeight] = useState(priority.new_release_weight);
  const [gapWeight, setGapWeight] = useState(priority.gap_weight);
  const [upgradeWeight, setUpgradeWeight] = useState(priority.upgrade_weight);
  const [newReleaseDays, setNewReleaseDays] = useState(priority.new_release_days);
  const [searchesPerHour, setSearchesPerHour] = useState(priority.searches_per_hour ?? 30);
  const [rssFreshHours, setRssFreshHours] = useState(priority.rss_fresh_hours ?? 48);
  const [huntMode, setHuntMode] = useState<HuntMode>(priority.hunt_mode ?? "oldest_searched");

  const dirty =
    userRequestWeight !== priority.user_request_weight ||
    newReleaseWeight !== priority.new_release_weight ||
    gapWeight !== priority.gap_weight ||
    upgradeWeight !== priority.upgrade_weight ||
    newReleaseDays !== priority.new_release_days ||
    searchesPerHour !== priority.searches_per_hour ||
    rssFreshHours !== (priority.rss_fresh_hours ?? 48) ||
    huntMode !== (priority.hunt_mode ?? "oldest_searched") ||
    minutes !== storedMinutes ||
    shares.some((share, index) => share.share !== priority.search_shares?.[index]?.share);
  const totalShare = shares.reduce((sum, share) => sum + share.share, 0);

  const submit = () =>
    onSave(
      {
      user_request_weight: userRequestWeight,
      new_release_weight: newReleaseWeight,
      gap_weight: gapWeight,
      upgrade_weight: upgradeWeight,
      new_release_days: newReleaseDays,
      searches_per_hour: searchesPerHour,
        rss_fresh_hours: rssFreshHours,
        hunt_mode: huntMode,
        search_shares: shares.map((share) => ({ library_id: share.library_id, share: share.share })),
      },
      minutes,
    );

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <Card
        title="Automatic searching"
        help="Missing items are searched on their own, most urgent first, the hour's searches spread over its runs. Keep the number low if an indexer limits API calls per day."
      >
        <div className="flex flex-wrap items-start gap-4">
          <Field label="Search every" help="How often a run starts." className="w-36">
            <select
              value={minutes ?? ""}
              disabled={busy}
              onChange={(event) => setMinutes(event.target.value ? Number(event.target.value) : undefined)}
              className={inputStyle}
            >
              {minutes === undefined && <option value="">Custom (Scheduler)</option>}
              {HUNT_MINUTES.map((choice) => (
                <option key={choice} value={choice}>
                  {choice === 60 ? "hour" : `${choice} minutes`}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Searches per hour" help="0 turns automatic searching off." className="w-44">
            <input
              type="number"
              min={0}
              max={1000}
              value={searchesPerHour}
              disabled={busy}
              onChange={(event) => setSearchesPerHour(Number(event.target.value))}
              className={inputStyle}
            />
          </Field>
          <Field
            label="Order"
            help="Each run's searches are split 40% new releases, 40% gaps and 20% upgrades; what one leaves unused goes to the others. This is the order within each share."
            className="min-w-64 flex-1"
          >
            <select
              value={huntMode}
              disabled={busy}
              onChange={(event) => setHuntMode(event.target.value as HuntMode)}
              className={`${inputStyle} max-w-sm`}
            >
              {HUNT_MODES.map(([id, label]) => (
                <option key={id} value={id}>
                  {label}
                </option>
              ))}
            </select>
          </Field>
        </div>
        {shares.length > 1 && (
          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-ink-muted">
              Each library&apos;s share of the searches, against the others&apos;: 2 gets twice as many as 1, 0 none. What
              one library cannot use goes to the others.
            </span>
            <div className="flex flex-wrap gap-3">
              {shares.map((share, index) => (
                <label key={share.library_id} className={labelStyle}>
                  {share.name ?? `Library ${share.library_id}`}
                  <span className="flex items-center gap-1.5">
                    <input
                      type="number"
                      min={0}
                      max={100}
                      value={share.share}
                      aria-label={`${share.name ?? `Library ${share.library_id}`} share`}
                      disabled={busy}
                      onChange={(event) =>
                        setShares(shares.map((entry, at) => (at === index ? { ...entry, share: Number(event.target.value) } : entry)))
                      }
                      className={`${inputStyle} w-20`}
                    />
                    <span className="w-10 tabular-nums">{totalShare > 0 ? `${Math.round((share.share / totalShare) * 100)}%` : "–"}</span>
                  </span>
                </label>
              ))}
            </div>
          </div>
        )}
      </Card>

      <Card
        title="Urgency"
        help="How urgently each kind of wish is fetched: a bigger weight is searched sooner. Waiting also raises an item's urgency, so nothing starves."
      >
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <WeightField label="User request" value={userRequestWeight} onChange={setUserRequestWeight} busy={busy} />
          <WeightField label="New release" value={newReleaseWeight} onChange={setNewReleaseWeight} busy={busy} />
          <WeightField label="Gap" value={gapWeight} onChange={setGapWeight} busy={busy} />
          <WeightField label="Upgrade" value={upgradeWeight} onChange={setUpgradeWeight} busy={busy} />
        </div>
        <Field
          label="Freshness window (days)"
          help="Days after release an item still counts as newly released rather than a gap."
          className="max-w-sm"
        >
          <input
            type="number"
            min={1}
            value={newReleaseDays}
            disabled={busy}
            onChange={(event) => setNewReleaseDays(Number(event.target.value))}
            className={`${inputStyle} w-28`}
          />
        </Field>
        <Field
          label="RSS freshness window (hours)"
          help="Hours after an RSS feed first shows a release for a wanted item that it still counts as newly arrived."
          className="max-w-sm"
        >
          <input
            type="number"
            min={1}
            value={rssFreshHours}
            disabled={busy}
            onChange={(event) => setRssFreshHours(Number(event.target.value))}
            className={`${inputStyle} w-28`}
          />
        </Field>
      </Card>

      <SaveBar dirty={dirty} busy={busy} onSave={submit} />
    </form>
  );
}

function WeightField({
  label,
  value,
  onChange,
  busy,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
  busy: boolean;
}) {
  return (
    <label className={labelStyle}>
      {label}
      <input
        type="number"
        min={0}
        value={value}
        disabled={busy}
        onChange={(event) => onChange(Number(event.target.value))}
        className={inputStyle}
      />
    </label>
  );
}
