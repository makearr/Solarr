import { type ReactNode, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { useRootFolders } from "@/lib/resources";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { ConfirmDialog } from "@/ui/ConfirmDialog";

type RootFolder = components["schemas"]["RootFolder"];
type DrainPlan = components["schemas"]["DrainPlan"];
type MoveJob = components["schemas"]["MoveJob"];

/** A root's rename and drain (ADR-51), part of its card (user, 2026-09-30): a rename is previewed, then confirmed; a
 *  drain is confirmed in a dialog that shows what it would move (2026-10-23). `children` are the card's other actions. */
export function RootMoveControls({ root, children }: { root: RootFolder; children?: ReactNode }) {
  const queryClient = useQueryClient();
  const [renamePlan, setRenamePlan] = useState<DrainPlan | null>(null);
  const [draining, setDraining] = useState(false);

  const previewRename = useMutation({
    mutationFn: () => ok(api.POST("/rootfolders/{id}/rename", { params: { path: { id: root.id } }, body: { preview: true } })),
    onSuccess: (data) => setRenamePlan(data ?? null),
  });
  const commitRename = useMutation({
    mutationFn: () => ok(api.POST("/rootfolders/{id}/rename", { params: { path: { id: root.id } }, body: { preview: false } })),
    onSuccess: (data) => {
      setRenamePlan(data ?? null);
      void queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
      void queryClient.invalidateQueries({ queryKey: ["move-jobs"] });
    },
  });
  const stop = useMutation({
    mutationFn: async () => {
      await ok(api.DELETE("/rootfolders/{id}/drain", { params: { path: { id: root.id } } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["rootfolders"] }),
  });

  const isDraining = root.drain_state === "DRAINING" || root.drain_state === "PAUSED";
  return (
    <div className="flex flex-col gap-1.5">
      <div className="-my-1 flex flex-wrap items-center justify-end gap-1">
        {isDraining ? (
          <Button variant="ghost" busy={stop.isPending} busyLabel="Stopping…" onClick={() => stop.mutate()}>
            Stop draining
          </Button>
        ) : (
          <>
            <Button variant="ghost" busy={previewRename.isPending} busyLabel="Computing…" disabled={!root.online} onClick={() => previewRename.mutate()}>
              Preview rename
            </Button>
            <Button variant="ghost" disabled={!root.online} title="Move every file to the library's other roots" onClick={() => setDraining(true)}>
              Drain…
            </Button>
          </>
        )}
        {children}
      </div>
      {previewRename.isError && <p className="text-xs text-danger">{problemDetail(previewRename.error, "The rename could not be planned.")}</p>}
      {renamePlan && (
        <PlanPreview
          plan={renamePlan}
          actionLabel="Rename"
          onCommit={() => commitRename.mutate()}
          committing={commitRename.isPending}
          onClose={() => setRenamePlan(null)}
        />
      )}
      {commitRename.isError && <p className="text-xs text-danger">{problemDetail(commitRename.error, "The rename was refused.")}</p>}
      {draining && <DrainDialog root={root} onClose={() => setDraining(false)} />}
    </div>
  );
}

/** The drain's confirmation: what would move where, then the drain itself; refused when the other roots cannot hold it. */
function DrainDialog({ root, onClose }: { root: RootFolder; onClose: () => void }) {
  const queryClient = useQueryClient();
  const preview = useQuery({
    queryKey: ["rootfolders", root.id, "drain-preview"],
    gcTime: 0,
    queryFn: () => ok(api.POST("/rootfolders/{id}/drain", { params: { path: { id: root.id } }, body: { preview: true } })),
  });
  const drain = useMutation({
    mutationFn: () => ok(api.POST("/rootfolders/{id}/drain", { params: { path: { id: root.id } }, body: { preview: false } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
      void queryClient.invalidateQueries({ queryKey: ["move-jobs"] });
      onClose();
    },
  });
  const roots = useRootFolders();
  const rootName = (id: number) => {
    const found = roots.data?.find((entry) => entry.id === id);
    return found ? found.label || found.path : `Root ${id}`;
  };
  const plan = preview.data;
  const refusal = plan?.refused[0]?.reason;
  const ready = plan !== undefined && refusal === undefined && plan.moves.length > 0;
  return (
    <ConfirmDialog
      title={`Drain ${root.label || root.path}?`}
      consequence="Every file on it moves to the library's other root folders, copied and verified first; nothing new is placed here while it drains. Stop draining halts it."
      confirmLabel={ready ? `Drain ${plan.files_total.toLocaleString()} ${plan.files_total === 1 ? "file" : "files"}` : "Drain"}
      danger
      busy={drain.isPending}
      disabled={!ready}
      onConfirm={() => drain.mutate()}
      onCancel={onClose}
    >
      <div className="flex flex-col gap-1 text-xs">
        {preview.isLoading && <p className="text-ink-muted">Working out what would move…</p>}
        {preview.isError && <p className="text-danger">{problemDetail(preview.error, "The drain could not be planned.")}</p>}
        {plan && refusal && <p className="text-danger">It cannot drain: {refusal}</p>}
        {plan && !refusal && plan.moves.length === 0 && <p className="text-ink-muted">Nothing on it to move.</p>}
        {ready && (
          <>
            <p>
              {plan.files_total.toLocaleString()} files, {formatBytes(plan.bytes_total)}, to{" "}
              {plan.root_deltas.filter((delta) => delta.bytes_in > 0).length} other root folders.
            </p>
            {plan.root_deltas
              .filter((delta) => delta.bytes_in > 0)
              .map((delta) => (
                <p key={delta.root_folder_id} className="text-ink-muted">
                  {rootName(delta.root_folder_id)}: {formatBytes(delta.bytes_in)} in, {formatBytes(delta.resulting_free_bytes)} free after
                </p>
              ))}
          </>
        )}
        {drain.isError && <p className="text-danger">{problemDetail(drain.error, "The drain was refused.")}</p>}
      </div>
    </ConfirmDialog>
  );
}

function problemDetail(error: unknown, fallback: string): string {
  return (error as { detail?: string } | null)?.detail ?? fallback;
}

/** A rename's preview: every file's current and proposed path, grouped by work; refused files are shown, not offered. */
function PlanPreview({
  plan,
  actionLabel,
  onCommit,
  committing,
  onClose,
}: {
  plan: DrainPlan;
  actionLabel: string;
  onCommit: () => void;
  committing: boolean;
  onClose: () => void;
}) {
  if (plan.accepted) {
    return (
      <div className="rounded-md border border-accent/40 bg-canvas p-2 text-xs text-ink-muted">
        Move job #{plan.move_job_id} started -- {plan.files_total} files, {formatBytes(plan.bytes_total)}.
      </div>
    );
  }
  const refused = new Set(plan.refused.map((entry) => entry.media_object_id));
  const runnable = plan.moves.filter((move) => !refused.has(move.media_object_id));
  const listed = new Set(plan.moves.map((move) => move.media_object_id));
  const leftOut = plan.refused.filter((entry) => !listed.has(entry.media_object_id));
  const firstRefusal = plan.refused.find((entry) => listed.has(entry.media_object_id));
  if (plan.moves.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-md border border-border-subtle bg-canvas p-2 text-xs text-ink-muted">
        {leftOut[0] ? (
          <span>
            Every other file already has its name; {leftOut.length} {leftOut.length === 1 ? "stays" : "stay"} as {leftOut.length === 1 ? "it is" : "they are"}:{" "}
            {leftOut[0].reason}
          </span>
        ) : (
          "Every file already has its name."
        )}
        <Button variant="ghost" className="ml-auto" onClick={onClose}>
          Close
        </Button>
      </div>
    );
  }

  // Every file's own current -> proposed path (section 11.8's "the preview is a diff"), an album's tracks together.
  const byGroup = new Map<string, { bytes: number; moves: typeof plan.moves }>();
  for (const move of plan.moves) {
    const existing = byGroup.get(move.affinity_key);
    if (existing) {
      existing.bytes += move.size_bytes;
      existing.moves.push(move);
    } else {
      byGroup.set(move.affinity_key, { bytes: move.size_bytes, moves: [move] });
    }
  }
  return (
    <div className="flex flex-col gap-2 rounded-md border border-border-subtle bg-canvas p-2 text-xs">
      {leftOut.length > 0 && (
        <p className="text-ink-muted">
          {leftOut.length} more {leftOut.length === 1 ? "file stays" : "files stay"} where {leftOut.length === 1 ? "it is" : "they are"}:{" "}
          {leftOut[0]!.reason}
        </p>
      )}
      {firstRefusal && (
        <p className="text-warn">
          {runnable.length === 0 ? "Shown, but nothing can be renamed" : `${plan.moves.length - runnable.length} of these cannot be renamed`}: {firstRefusal.reason}
        </p>
      )}
      <ul className="flex max-h-80 flex-col gap-2 overflow-y-auto">
        {[...byGroup.entries()].map(([key, group]) => (
          <li key={key} className="flex flex-col gap-0.5">
            <span className="text-ink-muted">
              {group.moves.length} file{group.moves.length === 1 ? "" : "s"}, {formatBytes(group.bytes)}
            </span>
            <ul className="flex flex-col gap-0.5 pl-2 text-[0.6875rem] text-ink-muted">
              {group.moves.map((move) => (
                <li key={move.media_object_id} className="flex flex-col">
                  <span className="truncate line-through opacity-60">{move.source_rel_path}</span>
                  <span className="truncate text-ink">{move.dest_rel_path}</span>
                  {(move.warnings ?? []).map((warning) => (
                    <span key={warning} className="pl-2 text-warn">
                      ⚠ {warning}
                    </span>
                  ))}
                  <Companions move={move} />
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
      <div className="flex items-center gap-2 text-ink-muted">
        <span>
          {plan.moves.length} files, {formatBytes(plan.moves.reduce((sum, move) => sum + move.size_bytes, 0))}
        </span>
        <span className="flex-1" />
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
        {runnable.length > 0 && (
          <Button variant="primary" busy={committing} busyLabel="Starting…" onClick={onCommit}>
            {actionLabel} {runnable.length} {runnable.length === 1 ? "file" : "files"}
          </Button>
        )}
      </div>
    </div>
  );
}

/** The files that move with a media file (subtitles, nfo, pictures), current then new name. */
export function Companions({ move }: { move: DrainPlan["moves"][number] }) {
  if (!move.companions?.length) return null;
  return (
    <span className="flex flex-col pl-3 text-[0.6875rem] text-ink-muted">
      {move.companions.map((companion) => (
        <span key={companion.source_rel_path} className="truncate" title={`${companion.source_rel_path} → ${companion.dest_rel_path}`}>
          + {companion.dest_rel_path.split("/").pop()}
        </span>
      ))}
    </span>
  );
}

export function MoveJobList() {
  const jobs = useQuery({
    queryKey: ["move-jobs"],
    queryFn: () => ok(api.GET("/moves")),
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
