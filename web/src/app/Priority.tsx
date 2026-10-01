import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

type PrioritySettings = components["schemas"]["PrioritySettings"];
type HuntMode = NonNullable<PrioritySettings["hunt_mode"]>;

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
    queryFn: async () => {
      const { data, error } = await api.GET("/priority");
      if (error) throw error;
      return data;
    },
  });

  const update = useMutation({
    mutationFn: async (body: PrioritySettings) => {
      const { error } = await api.PATCH("/priority", { body });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["priority"] }),
  });

  if (!isTierVisible(tier, "advanced")) return null;
  if (priority.error !== null || !priority.data) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Priority</h2>
      <p className="text-xs text-ink-muted">
        How urgently something is fetched: four trigger weights (bigger is more urgent) and
        how long a release counts as newly released before it competes as a gap instead.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <PriorityForm priority={priority.data} onSave={(body) => update.mutate(body)} busy={update.isPending} />
        {update.isError && <p className="text-xs text-danger">That change was refused.</p>}
      </div>
    </section>
  );
}

function PriorityForm({
  priority,
  onSave,
  busy,
}: {
  priority: PrioritySettings;
  onSave: (body: PrioritySettings) => void;
  busy: boolean;
}) {
  const [userRequestWeight, setUserRequestWeight] = useState(priority.user_request_weight);
  const [newReleaseWeight, setNewReleaseWeight] = useState(priority.new_release_weight);
  const [gapWeight, setGapWeight] = useState(priority.gap_weight);
  const [upgradeWeight, setUpgradeWeight] = useState(priority.upgrade_weight);
  const [newReleaseDays, setNewReleaseDays] = useState(priority.new_release_days);
  const [searchesPerHour, setSearchesPerHour] = useState(priority.searches_per_hour ?? 12);
  const [huntMode, setHuntMode] = useState<HuntMode>(priority.hunt_mode ?? "oldest_searched");

  const dirty =
    userRequestWeight !== priority.user_request_weight ||
    newReleaseWeight !== priority.new_release_weight ||
    gapWeight !== priority.gap_weight ||
    upgradeWeight !== priority.upgrade_weight ||
    newReleaseDays !== priority.new_release_days ||
    searchesPerHour !== priority.searches_per_hour ||
    huntMode !== (priority.hunt_mode ?? "oldest_searched");

  return (
    <form
      className="flex flex-col gap-3 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        onSave({
          user_request_weight: userRequestWeight,
          new_release_weight: newReleaseWeight,
          gap_weight: gapWeight,
          upgrade_weight: upgradeWeight,
          new_release_days: newReleaseDays,
          searches_per_hour: searchesPerHour,
          hunt_mode: huntMode,
        });
      }}
    >
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <WeightField label="User request" value={userRequestWeight} onChange={setUserRequestWeight} busy={busy} />
        <WeightField label="New release" value={newReleaseWeight} onChange={setNewReleaseWeight} busy={busy} />
        <WeightField label="Gap" value={gapWeight} onChange={setGapWeight} busy={busy} />
        <WeightField label="Upgrade" value={upgradeWeight} onChange={setUpgradeWeight} busy={busy} />
      </div>

      <label className="flex flex-col gap-1">
        Freshness window (days)
        <input
          type="number"
          min={1}
          value={newReleaseDays}
          disabled={busy}
          onChange={(event) => setNewReleaseDays(Number(event.target.value))}
          className={`${inputStyle} w-24`}
        />
        <span className="text-[0.6875rem]">
          Days after release an item still counts as newly released rather than a gap.
        </span>
      </label>

      <label className="flex flex-col gap-1">
        Automatic searches per hour
        <input
          type="number"
          min={0}
          max={1000}
          value={searchesPerHour}
          disabled={busy}
          onChange={(event) => setSearchesPerHour(Number(event.target.value))}
          className={`${inputStyle} w-24`}
        />
        <span className="text-[0.6875rem]">
          Missing albums searched on their own each hour, most urgent first. 0 turns automatic
          searching off. Keep it low if an indexer limits API calls per day.
        </span>
      </label>

      <label className="flex flex-col gap-1">
        Automatic search order
        <select
          value={huntMode}
          disabled={busy}
          onChange={(event) => setHuntMode(event.target.value as HuntMode)}
          className={`${inputStyle} w-64`}
        >
          {HUNT_MODES.map(([id, label]) => (
            <option key={id} value={id}>
              {label}
            </option>
          ))}
        </select>
        <span className="text-[0.6875rem]">
          Each hour's searches are split 40% new releases, 40% gaps and 20% upgrades; what one leaves unused goes to
          the others. This is the order within each share.
        </span>
      </label>

      {dirty && (
        <button
          type="submit"
          disabled={busy}
          className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
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
    <label className="flex flex-col gap-1">
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

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
