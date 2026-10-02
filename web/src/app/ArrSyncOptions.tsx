import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Plus, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type ArrInstance = components["schemas"]["ArrInstance"];
type ArrImportRun = components["schemas"]["ArrImportRun"];
type Rewrite = { from: string; to: string };

const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs";
const button = "rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50";

/** What one Lidarr import brings in (asked 2026-09-30): its library's folders, monitored and unmonitored
 *  items, which albums, and how its paths read here. */
export function ArrSyncOptions({
  instance,
  onStarted,
  onCancel,
}: {
  instance: ArrInstance;
  onStarted: (run: ArrImportRun) => void;
  onCancel: () => void;
}) {
  const [monitored, setMonitored] = useState(true);
  const [unmonitored, setUnmonitored] = useState(false);
  const [albums, setAlbums] = useState<"all" | "missing" | "complete">("all");
  const [rewrites, setRewrites] = useState<Rewrite[]>([]);
  const [importFolders, setImportFolders] = useState(false);
  const [libraryId, setLibraryId] = useState<number | undefined>(instance.category_id ?? undefined);
  const [notes, setNotes] = useState<string[]>([]);

  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  const folders = useQuery({
    queryKey: ["arr-rootfolders", instance.id],
    queryFn: async () => {
      const { data, error } = await api.GET("/arr/instances/{id}/rootfolders", { params: { path: { id: instance.id } } });
      if (error) throw error;
      return data;
    },
  });
  const rewrite = (path: string) => {
    for (const { from, to } of rewrites) {
      const prefix = from.replace(/\/+$/, "");
      if (prefix && (path === prefix || path.startsWith(`${prefix}/`))) return to.replace(/\/+$/, "") + path.slice(prefix.length);
    }
    return path;
  };

  const start = useMutation({
    mutationFn: async () => {
      const problems: string[] = [];
      // The library's folders first, so the import below finds each artist's library by them.
      if (importFolders && libraryId !== undefined) {
        for (const folder of folders.data ?? []) {
          const path = rewrite(folder.path);
          const { data: root, error } = await api.POST("/rootfolders", { body: { path } });
          if (error || !root) {
            problems.push(`${path}: ${(error as { detail?: string } | undefined)?.detail ?? "could not be used here"}`);
            continue;
          }
          const attached = await api.POST("/categories/{id}/rootfolders", {
            params: { path: { id: libraryId } },
            body: { root_folder_id: root.id },
          });
          if (attached.error) problems.push(`${path}: added, but not to the library`);
        }
      }
      const { data, error } = await api.POST("/arr/instances/{id}/sync", {
        params: { path: { id: instance.id } },
        body: { monitored, unmonitored, albums, path_rewrites: rewrites.filter((entry) => entry.from.trim() && entry.to.trim()) },
      });
      if (error) throw error;
      if (importFolders) await api.POST("/schedules/{key}/run", { params: { path: { key: "scan" } } });
      setNotes(problems);
      return data;
    },
    onSuccess: onStarted,
  });

  const musicLibraries = (libraries.data ?? []).filter((library) => library.media_type === "music");
  return (
    <section className="flex flex-col gap-2 rounded-md border border-border-subtle bg-canvas p-3 text-xs">
      <h3 className="font-medium text-ink">Import from {instance.name}</h3>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={importFolders} onChange={(event) => setImportFolders(event.target.checked)} />
        Its library: add its root folders
        <select
          aria-label="Into which library"
          value={libraryId ?? ""}
          disabled={!importFolders}
          onChange={(event) => setLibraryId(event.target.value ? Number(event.target.value) : undefined)}
          className={input}
        >
          <option value="">Choose a library</option>
          {musicLibraries.map((library) => (
            <option key={library.id} value={library.id}>
              {library.name}
            </option>
          ))}
        </select>
        and scan them
      </label>
      {importFolders && (folders.data ?? []).length > 0 && (
        <p className="pl-6 font-mono text-ink-muted">{(folders.data ?? []).map((folder) => rewrite(folder.path)).join(" · ")}</p>
      )}
      <div className="flex flex-col gap-1">
        <span className="text-ink-muted">Its paths read here as (Lidarr's /media/music is /mnt/media/music here, say)</span>
        {rewrites.map((entry, index) => (
          <div key={index} className="flex items-center gap-1">
            <input className={input} placeholder="/media/music" value={entry.from}
              onChange={(event) => setRewrites(rewrites.map((old, i) => (i === index ? { ...old, from: event.target.value } : old)))} />
            <span className="text-ink-muted">→</span>
            <input className={input} placeholder="/mnt/media/music" value={entry.to}
              onChange={(event) => setRewrites(rewrites.map((old, i) => (i === index ? { ...old, to: event.target.value } : old)))} />
            <button type="button" aria-label="Remove this path" onClick={() => setRewrites(rewrites.filter((_, i) => i !== index))} className={button}>
              <X aria-hidden className="size-3" />
            </button>
          </div>
        ))}
        <button type="button" onClick={() => setRewrites([...rewrites, { from: "", to: "" }])} className={`${button} flex w-fit items-center gap-1`}>
          <Plus aria-hidden className="size-3" /> Add a path
        </button>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={monitored} onChange={(event) => setMonitored(event.target.checked)} />
          {instance.app === "sonarr" ? "Monitored series" : "Monitored artists and albums"}
        </label>
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={unmonitored} onChange={(event) => setUnmonitored(event.target.checked)} />
          Unmonitored ones (they stay unmonitored)
        </label>
        {instance.app !== "sonarr" && (
        <label className="flex items-center gap-1.5">
          Albums
          <select value={albums} onChange={(event) => setAlbums(event.target.value as typeof albums)} className={input}>
            <option value="all">All</option>
            <option value="missing">Missing tracks in Lidarr</option>
            <option value="complete">Complete in Lidarr</option>
          </select>
        </label>
        )}
      </div>
      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={(!monitored && !unmonitored) || (importFolders && libraryId === undefined) || start.isPending}
          onClick={() => start.mutate()}
          className="rounded-md bg-accent px-3 py-1 text-xs text-accent-ink disabled:opacity-50"
        >
          {start.isPending ? "Starting…" : "Start import"}
        </button>
        <button type="button" onClick={onCancel} className={button}>
          Cancel
        </button>
        {start.isError && <span className="text-danger">{(start.error as { detail?: string }).detail ?? "The import could not start."}</span>}
      </div>
      {notes.map((note) => (
        <p key={note} className="text-warn">{note}</p>
      ))}
    </section>
  );
}
