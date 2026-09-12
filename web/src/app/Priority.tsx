import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

type PrioritySettings = components["schemas"]["PrioritySettings"];

/** todo 4.7, ADR-36/ADR-55: the four trigger weights and the release-date
 *  freshness window. Settings only -- internal/identify/priority.Compute has
 *  no caller until stage 5/6's planner exists, so there is no waiting item
 *  yet to show a live priority breakdown for. Advanced tier (the registry's
 *  own priority.* keys, todo 4.14), same reasoning as Network's own. */
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
        ADR-36's composite acquisition priority: four trigger weights (bigger is more urgent) and
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

  const dirty =
    userRequestWeight !== priority.user_request_weight ||
    newReleaseWeight !== priority.new_release_weight ||
    gapWeight !== priority.gap_weight ||
    upgradeWeight !== priority.upgrade_weight ||
    newReleaseDays !== priority.new_release_days;

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
          Days after release an item still counts as newly released rather than a gap (ADR-55).
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
