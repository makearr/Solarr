import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type RootFolder = components["schemas"]["RootFolder"];
type DrainPlan = components["schemas"]["DrainPlan"];
type MoveJob = components["schemas"]["MoveJob"];

/** ADR-51's own drain workflow: preview which files would move where and
 *  what each root ends up with, then commit -- the same preview-then-commit
 *  shape Library.tsx's bulk monitor action already uses. Rename, category
 *  migration and rebalance share the same move engine but have no screen of
 *  their own yet -- drain was the user's own original request. */
export function Moves() {
  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Move, rebalance and drain</h2>
        <span className="text-xs text-ink-muted">
          Evacuate a root onto its siblings, grouped by artist so nothing splits across disks
        </span>
      </div>

      <RootFolderList />
      <MoveJobList />
    </div>
  );
}

function RootFolderList() {
  const roots = useQuery({
    queryKey: ["rootfolders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/rootfolders");
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="rounded-lg border border-border-subtle bg-surface">
      <h3 className="border-b border-border-subtle px-3 py-2 text-xs font-medium text-ink-muted">
        Root folders
      </h3>
      {roots.isLoading && <Message>Loading root folders…</Message>}
      {roots.error !== null && <Message>Root folders could not be read.</Message>}
      {roots.data && roots.data.length === 0 && <Message>No root folders configured.</Message>}
      {roots.data && roots.data.length > 0 && (
        <ul className="divide-y divide-border-subtle">
          {roots.data.map((root) => (
            <RootFolderRow key={root.id} root={root} />
          ))}
        </ul>
      )}
    </div>
  );
}

function RootFolderRow({ root }: { root: RootFolder }) {
  const queryClient = useQueryClient();
  const [plan, setPlan] = useState<DrainPlan | null>(null);
  const [planError, setPlanError] = useState(false);

  const preview = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/rootfolders/{id}/drain", {
        params: { path: { id: root.id } },
        body: { preview: true },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => {
      setPlanError(false);
      setPlan(data);
    },
    onError: () => setPlanError(true),
  });

  const commit = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/rootfolders/{id}/drain", {
        params: { path: { id: root.id } },
        body: { preview: false },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => {
      setPlan(data);
      queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
      queryClient.invalidateQueries({ queryKey: ["move-jobs"] });
    },
  });

  const stop = useMutation({
    mutationFn: async () => {
      const { error } = await api.DELETE("/rootfolders/{id}/drain", {
        params: { path: { id: root.id } },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setPlan(null);
      queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
    },
  });

  const draining = root.drain_state === "DRAINING" || root.drain_state === "PAUSED";

  return (
    <li className="flex flex-col gap-2 px-3 py-2.5 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="truncate font-mono text-sm text-ink" title={root.path}>
          {root.label || root.path}
        </span>
        {root.drain_state && root.drain_state !== "NONE" && <DrainStateBadge state={root.drain_state} />}
        {!root.online && (
          <span className="rounded-full border border-danger px-1.5 py-px text-[0.6875rem] text-danger">
            offline
          </span>
        )}
        <span className="flex-1" />
        {draining ? (
          <button
            type="button"
            onClick={() => stop.mutate()}
            disabled={stop.isPending}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:border-danger hover:text-danger
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {stop.isPending ? "Stopping…" : "Stop draining"}
          </button>
        ) : (
          <button
            type="button"
            onClick={() => preview.mutate()}
            disabled={preview.isPending || !root.online}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink hover:border-accent
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {preview.isPending ? "Computing…" : "Preview drain"}
          </button>
        )}
      </div>
      {planError && <p className="text-danger">That could not be computed.</p>}
      {plan && <DrainPreview plan={plan} onCommit={() => commit.mutate()} committing={commit.isPending} />}
    </li>
  );
}

function DrainPreview({
  plan,
  onCommit,
  committing,
}: {
  plan: DrainPlan;
  onCommit: () => void;
  committing: boolean;
}) {
  if (plan.accepted) {
    return (
      <div className="rounded-md border border-accent/40 bg-canvas p-2 text-ink-muted">
        Move job #{plan.move_job_id} started -- {plan.files_total} files, {formatBytes(plan.bytes_total)}.
      </div>
    );
  }

  const firstRefusal = plan.refused[0];
  if (firstRefusal) {
    return (
      <div className="flex flex-col gap-1 rounded-md border border-danger/40 bg-canvas p-2">
        <span className="text-danger">Refused -- nothing was moved.</span>
        <span className="text-ink-muted">{firstRefusal.reason}</span>
      </div>
    );
  }

  if (plan.moves.length === 0) {
    return <div className="rounded-md border border-border-subtle bg-canvas p-2 text-ink-muted">Nothing to drain.</div>;
  }

  const byGroup = new Map<string, { files: number; bytes: number; destRootFolderId: number }>();
  for (const move of plan.moves) {
    const existing = byGroup.get(move.affinity_key);
    if (existing) {
      existing.files += 1;
      existing.bytes += move.size_bytes;
    } else {
      byGroup.set(move.affinity_key, { files: 1, bytes: move.size_bytes, destRootFolderId: move.dest_root_folder_id });
    }
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border border-border-subtle bg-canvas p-2">
      <ul className="flex flex-col gap-1">
        {[...byGroup.entries()].map(([key, group]) => (
          <li key={key} className="flex items-center gap-2 text-ink-muted">
            <span className="truncate font-mono text-ink">{key}</span>
            <span>
              {group.files} file{group.files === 1 ? "" : "s"}, {formatBytes(group.bytes)} → root{" "}
              {group.destRootFolderId}
            </span>
          </li>
        ))}
      </ul>
      <div className="flex items-center gap-2 text-ink-muted">
        <span>
          {plan.files_total} files total, {formatBytes(plan.bytes_total)}
        </span>
        <span className="flex-1" />
        <button
          type="button"
          onClick={onCommit}
          disabled={committing}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {committing ? "Starting…" : "Confirm drain"}
        </button>
      </div>
    </div>
  );
}

function MoveJobList() {
  const jobs = useQuery({
    queryKey: ["move-jobs"],
    queryFn: async () => {
      const { data, error } = await api.GET("/moves");
      if (error) throw error;
      return data;
    },
    refetchInterval: 5000,
  });

  return (
    <div className="rounded-lg border border-border-subtle bg-surface">
      <h3 className="border-b border-border-subtle px-3 py-2 text-xs font-medium text-ink-muted">Move jobs</h3>
      {jobs.isLoading && <Message>Loading move jobs…</Message>}
      {jobs.error !== null && <Message>Move jobs could not be read.</Message>}
      {jobs.data && jobs.data.length === 0 && <Message>No move has run yet.</Message>}
      {jobs.data && jobs.data.length > 0 && (
        <ul className="divide-y divide-border-subtle">
          {jobs.data.map((job) => (
            <MoveJobRow key={job.id} job={job} />
          ))}
        </ul>
      )}
    </div>
  );
}

function MoveJobRow({ job }: { job: MoveJob }) {
  const fraction = job.bytes_total > 0 ? job.bytes_done / job.bytes_total : 0;
  return (
    <li className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
          {job.reason}
        </span>
        <MoveJobStateBadge state={job.state} />
        {job.source_root_id !== undefined && (
          <span className="text-ink-muted">from root {job.source_root_id}</span>
        )}
        <span className="flex-1" />
        <span className="text-ink-muted">
          {job.files_done}/{job.files_total} files
        </span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-canvas">
        <div className="h-full rounded-full bg-accent" style={{ width: `${Math.round(fraction * 100)}%` }} />
      </div>
      {job.error && <span className="text-danger">{job.error}</span>}
    </li>
  );
}

function DrainStateBadge({ state }: { state: NonNullable<RootFolder["drain_state"]> }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "DRAINED"
          ? "border-border-subtle text-ink-muted"
          : "border-accent text-accent"
      }`}
    >
      {state}
    </span>
  );
}

function MoveJobStateBadge({ state }: { state: MoveJob["state"] }) {
  const isTerminal = state === "DONE" || state === "FAILED";
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "FAILED"
          ? "border-danger text-danger"
          : isTerminal
            ? "border-border-subtle text-ink-muted"
            : "border-accent text-accent"
      }`}
    >
      {state}
    </span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-3 text-xs text-ink-muted">{children}</p>;
}
