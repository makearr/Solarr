import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HardDrive, Plus } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type RootFolder = components["schemas"]["RootFolder"];

/** todo 4.18: the root folder overview screen that never existed --
 *  internal/capacity has computed used/free/total for every root since
 *  2026-09-04 and nothing rendered it until now. Every number here is what
 *  the last capacity sweep found, not a live stat -- "capacity is polled,
 *  not assumed" (section 11.7). */
export function RootFolders() {
  const queryClient = useQueryClient();
  const roots = useQuery({
    queryKey: ["rootfolders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/rootfolders");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["rootfolders"] });

  const setPolicy = useMutation({
    mutationFn: async (args: { id: number; readOnly: boolean; allowRename: boolean; minFreeBytes: number }) => {
      const { error } = await api.PATCH("/rootfolders/{id}", {
        params: { path: { id: args.id } },
        body: { read_only: args.readOnly, allow_rename: args.allowRename, min_free_bytes: args.minFreeBytes },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const setOnline = useMutation({
    mutationFn: async (args: { id: number; online: boolean }) => {
      const { error } = await api.PATCH("/rootfolders/{id}/online", {
        params: { path: { id: args.id } },
        body: { online: args.online },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Root folders</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Every library root, its own placement policy, and what the last capacity sweep found.
          A root declared offline stays that way until declared online again -- a disk coming
          back on its own never clears it (section 10.4, Invariant 87).
        </p>
      </header>

      {roots.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {roots.error !== null && <p className="text-sm text-danger">Root folders could not be read.</p>}

      {roots.data && roots.data.length > 0 ? (
        <ul className="flex flex-col gap-3">
          {roots.data.map((root) => (
            <RootFolderCard
              key={root.id}
              root={root}
              onSavePolicy={(readOnly, allowRename, minFreeBytes) =>
                setPolicy.mutate({ id: root.id, readOnly, allowRename, minFreeBytes })
              }
              onToggleOnline={() => setOnline.mutate({ id: root.id, online: !root.online })}
              busy={setPolicy.isPending || setOnline.isPending}
            />
          ))}
        </ul>
      ) : (
        !roots.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No library root is configured.
          </p>
        )
      )}

      {(setPolicy.isError || setOnline.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddRootFolder onAdded={invalidate} />
    </div>
  );
}

function RootFolderCard({
  root,
  onSavePolicy,
  onToggleOnline,
  busy,
}: {
  root: RootFolder;
  onSavePolicy: (readOnly: boolean, allowRename: boolean, minFreeBytes: number) => void;
  onToggleOnline: () => void;
  busy: boolean;
}) {
  const [readOnly, setReadOnly] = useState(root.read_only ?? false);
  const [allowRename, setAllowRename] = useState(root.allow_rename ?? false);
  const [minFreeBytes, setMinFreeBytes] = useState(root.min_free_bytes ?? 0);
  const dirty =
    readOnly !== (root.read_only ?? false) ||
    allowRename !== (root.allow_rename ?? false) ||
    minFreeBytes !== (root.min_free_bytes ?? 0);

  const total = root.total_bytes ?? 0;
  const free = root.free_bytes ?? 0;
  const used = total > 0 ? total - free : 0;
  const usedPct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const reservePct = total > 0 ? Math.min(100, Math.round(((root.min_free_bytes ?? 0) / total) * 100)) : 0;

  return (
    <li className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <HardDrive aria-hidden className="size-4 shrink-0 text-ink-muted" />
        <span className="font-medium">{root.label || root.path}</span>
        <span className="truncate font-mono text-xs text-ink-muted">{root.path}</span>

        <span className="ml-auto flex shrink-0 items-center gap-1.5">
          <Badge tone={root.online ? "ok" : "warn"}>{root.online ? "online" : "offline"}</Badge>
          {root.writable !== undefined && (
            <Badge tone={root.writable ? "ok" : "warn"}>{root.writable ? "writable" : "not writable"}</Badge>
          )}
          {root.drain_state && root.drain_state !== "NONE" && <Badge tone="warn">{root.drain_state}</Badge>}
        </span>
      </div>

      {root.categories && root.categories.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {root.categories.map((category) => (
            <span
              key={category.id}
              className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted"
            >
              {category.name}
            </span>
          ))}
        </div>
      )}

      {total > 0 ? (
        <div className="flex flex-col gap-1">
          <div className="relative h-2 overflow-hidden rounded-full bg-canvas">
            <div className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${usedPct}%` }} />
            {reservePct > 0 && (
              <div
                className="absolute inset-y-0 border-l-2 border-dashed border-danger/70"
                style={{ left: `${100 - reservePct}%` }}
              />
            )}
          </div>
          <span className="text-xs text-ink-muted">
            {formatBytes(used)} used of {formatBytes(total)} ({formatBytes(free)} free)
            {root.capacity_checked_at && ` · measured ${new Date(root.capacity_checked_at).toLocaleString()}`}
          </span>
        </div>
      ) : (
        <span className="text-xs text-ink-muted">Not measured yet -- the capacity sweep has not run.</span>
      )}

      <div className="flex flex-wrap items-center gap-3 border-t border-border-subtle pt-2.5 text-xs">
        <label className="flex items-center gap-1.5 text-ink-muted">
          <input
            type="checkbox"
            checked={readOnly}
            disabled={busy}
            onChange={(event) => setReadOnly(event.target.checked)}
            className="size-3.5"
          />
          read only
        </label>
        <label className="flex items-center gap-1.5 text-ink-muted">
          <input
            type="checkbox"
            checked={allowRename}
            disabled={busy}
            onChange={(event) => setAllowRename(event.target.checked)}
            className="size-3.5"
          />
          allow rename
        </label>
        <label className="flex items-center gap-1.5 text-ink-muted">
          reserve
          <input
            type="number"
            min={0}
            value={Math.round(minFreeBytes / 1_000_000_000)}
            disabled={busy}
            onChange={(event) => setMinFreeBytes(Number(event.target.value) * 1_000_000_000)}
            className="w-16 rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
          GB
        </label>

        {dirty && (
          <button
            type="button"
            onClick={() => onSavePolicy(readOnly, allowRename, minFreeBytes)}
            disabled={busy}
            className="rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Save
          </button>
        )}

        <button
          type="button"
          onClick={onToggleOnline}
          disabled={busy}
          className="ml-auto rounded-md border border-border-subtle px-2 py-1 text-ink-muted
                     hover:text-ink disabled:opacity-50 focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {root.online ? "Declare offline" : "Declare online"}
        </button>
      </div>
    </li>
  );
}

function AddRootFolder({ onAdded }: { onAdded: () => void }) {
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/rootfolders", { body: { path, label: label || undefined } });
      if (error) throw error;
    },
    onSuccess: () => {
      setPath("");
      setLabel("");
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Path
        <input
          className={inputStyle}
          value={path}
          placeholder="/media/music"
          autoComplete="off"
          onChange={(event) => setPath(event.target.value)}
        />
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Label
        <input
          className={inputStyle}
          value={label}
          placeholder="music-hdd-1"
          autoComplete="off"
          onChange={(event) => setLabel(event.target.value)}
        />
      </label>
      <button
        type="submit"
        disabled={!path.trim() || create.isPending}
        className="flex items-center gap-1 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Plus aria-hidden className="size-3.5" />
        {create.isPending ? "Adding…" : "Add root folder"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That path could not be used -- check it exists and is mounted inside the container.
        </p>
      )}
    </form>
  );
}

function Badge({ tone, children }: { tone: "ok" | "warn"; children: React.ReactNode }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        tone === "ok" ? "border-border-subtle text-ink-muted" : "border-warn text-warn"
      }`}
    >
      {children}
    </span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`;
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
