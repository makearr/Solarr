import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { BellRing } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibrary } from "@/lib/useLibrary";

type Follow = components["schemas"]["SeriesFollow"];
type Monitor = NonNullable<components["schemas"]["SeriesFollowRequest"]["monitor"]>;

/** A person's roles as the follow offers them: each choice stands for the roles it covers. */
const ROLE_CHOICES: { label: string; roles: string[] }[] = [
  { label: "Acting", roles: ["actor"] },
  { label: "Creating and running", roles: ["creator", "showrunner"] },
  { label: "Directing", roles: ["director"] },
  { label: "Writing", roles: ["writer"] },
  { label: "Producing", roles: ["executive_producer", "producer"] },
  { label: "Composing", roles: ["composer"] },
];

const MONITORS: { value: Monitor; label: string }[] = [
  { value: "future", label: "Episodes that have not aired yet" },
  { value: "all", label: "Every episode" },
  { value: "first_season", label: "The first season" },
  { value: "none", label: "Nothing (add them unmonitored)" },
];

/** "Add their new series for me": a follow of a person (by role) or a network, made from its own page. */
export function FollowPanel({
  kind,
  subjectId,
  categoryId,
  follow,
  followable,
  suggestedRoles,
}: {
  kind: "person" | "network";
  subjectId: number;
  categoryId: string;
  follow?: Follow;
  followable?: boolean;
  suggestedRoles?: string[];
}) {
  const canManage = useLibrary(categoryId)?.permission === "manage";
  const queryClient = useQueryClient();
  const base = kind === "person" ? "/series/people/{id}/follow" : "/series/networks/{id}/follow";
  const initialRoles = follow?.roles?.length ? follow.roles : suggestedRoles ?? [];
  const [chosen, setChosen] = useState<string[]>(initialRoles);
  const [monitor, setMonitor] = useState<Monitor>((follow?.monitor as Monitor | undefined) ?? "future");
  const [back, setBack] = useState(follow?.back_catalogue ?? false);
  const [open, setOpen] = useState(false);

  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: [kind === "person" ? "series-person" : "series-network", categoryId, String(subjectId)] });
  };
  const save = useMutation({
    mutationFn: async () => {
      const { error } = await api.PUT(base, {
        params: { path: { id: subjectId } },
        body: { category_id: Number(categoryId), roles: kind === "person" ? chosen : undefined, monitor, back_catalogue: back },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      changed();
      setOpen(false);
    },
  });
  const stop = useMutation({
    mutationFn: async () => {
      const { error } = await api.DELETE(base, { params: { path: { id: subjectId }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
    },
    onSuccess: changed,
  });

  if (!canManage || followable === false) return null;
  const toggle = (roles: string[]) =>
    setChosen((current) => (roles.every((role) => current.includes(role)) ? current.filter((role) => !roles.includes(role)) : [...new Set([...current, ...roles])]));

  return (
    <div className="flex flex-col gap-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <BellRing aria-hidden className={`size-4 ${follow ? "text-accent" : "text-ink-muted"}`} />
        {follow ? (
          <span>
            Following{kind === "person" && follow.roles.length > 0 ? ` (${follow.roles.map((role) => role.replace(/_/g, " ")).join(", ")})` : ""}: new series are added
            automatically.
          </span>
        ) : (
          <span className="text-ink-muted">Not following: new series {kind === "person" ? "of theirs" : "on this network"} are not added for you.</span>
        )}
        <button type="button" onClick={() => setOpen(!open)} className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink">
          {follow ? "Change" : "Follow"}
        </button>
        {follow && (
          <button
            type="button"
            disabled={stop.isPending}
            onClick={() => stop.mutate()}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink disabled:opacity-50"
          >
            Unfollow
          </button>
        )}
      </div>
      {open && (
        <div className="flex max-w-xl flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
          {kind === "person" && (
            <fieldset className="flex flex-col gap-1">
              <legend className="mb-1 text-ink-muted">Follow them as</legend>
              {ROLE_CHOICES.map((choice) => (
                <label key={choice.label} className="flex items-center gap-2">
                  <input type="checkbox" checked={choice.roles.every((role) => chosen.includes(role))} onChange={() => toggle(choice.roles)} />
                  {choice.label}
                </label>
              ))}
              <span className="text-[0.6875rem] text-ink-muted">Nothing ticked means any role.</span>
            </fieldset>
          )}
          <label className="flex flex-col gap-1">
            <span className="text-ink-muted">Monitor, for each series it adds</span>
            <select value={monitor} onChange={(event) => setMonitor(event.target.value as Monitor)} className="max-w-xs rounded-md border border-border-subtle bg-canvas px-2 py-1">
              {MONITORS.map((entry) => (
                <option key={entry.value} value={entry.value}>
                  {entry.label}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={back} onChange={(event) => setBack(event.target.checked)} />
            Also add older series (up to 25 at a time), not only new ones
          </label>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={save.isPending}
              onClick={() => save.mutate()}
              className="rounded-md bg-accent px-3 py-1 text-accent-ink disabled:opacity-50"
            >
              {follow ? "Save" : "Follow"}
            </button>
            {save.isError && <span className="text-danger">That could not be saved.</span>}
          </div>
        </div>
      )}
    </div>
  );
}
