import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

import { formatBytes } from "./Queue";

const GB = 1024 * 1024 * 1024;

/** The recycle bin's space bound, for every library (asked 2026-09-30); each library keeps its own
 *  retention in days under its File Management. */
export function RecycleBin() {
  const queryClient = useQueryClient();
  const limits = useQuery({
    queryKey: ["recycleBinLimits"],
    queryFn: () => ok(api.GET("/recyclebin")),
  });
  const [maxGB, setMaxGB] = useState(0);
  const [percent, setPercent] = useState(0);
  useEffect(() => {
    if (!limits.data) return;
    setMaxGB(Math.round(limits.data.max_bytes / GB));
    setPercent(limits.data.max_percent_of_root);
  }, [limits.data]);

  const contents = useQuery({
    queryKey: ["recycleBinEntries"],
    queryFn: () => ok(api.GET("/recyclebin/entries")),
  });
  const empty = useMutation({
    mutationFn: () => ok(api.POST("/recyclebin/empty")),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["recycleBinEntries"] }),
  });

  const save = useMutation({
    mutationFn: () => ok(api.PATCH("/recyclebin", {
      body: { max_bytes: maxGB * GB, max_percent_of_root: percent },
    })),
    onSuccess: (data) => queryClient.setQueryData(["recycleBinLimits"], data),
  });

  if (!limits.data) return <p className="p-6 text-sm text-ink-muted">{limits.isError ? "The limits could not be read." : "Loading…"}</p>;
  const dirty = maxGB * GB !== limits.data.max_bytes || percent !== limits.data.max_percent_of_root;

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">Recycle bin</h1>
      <p className="text-xs text-ink-muted">
        Nothing Solarr removes is deleted at once: it goes to its root folder&apos;s recycle bin and is removed after
        the library&apos;s own days (Library → File Management), or sooner, oldest first, once a limit below is passed.
      </p>
      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4 text-sm">
        <label className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="w-56 shrink-0">All recycle bins together</span>
          <input type="number" min={0} value={maxGB} onChange={(event) => setMaxGB(Math.max(0, Number(event.target.value)))} className={`${inputStyle} w-24`} />
          <span className="text-xs text-ink-muted">GB; 0 is no limit.</span>
        </label>
        <label className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="w-56 shrink-0">One root folder&apos;s recycle bin</span>
          <input type="number" min={0} max={100} value={percent} onChange={(event) => setPercent(Number(event.target.value))} className={`${inputStyle} w-24`} />
          <span className="text-xs text-ink-muted">% of the root folder; 0 is no limit.</span>
        </label>
      </section>
      <div className="flex items-center gap-2">
        <Button
          variant="primary"
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate()}
        >
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {save.isError && <span className="text-xs text-danger">That was not saved -- the limits have a problem.</span>}
        {save.isSuccess && !dirty && <span className="text-xs text-ink-muted">Saved; the next cleanup applies it.</span>}
      </div>
      <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4 text-sm">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="font-medium">
            In the bin{contents.data ? `: ${contents.data.total_entries} files, ${formatBytes(contents.data.total_bytes)}` : ""}
          </h2>
          <button
            type="button"
            disabled={!contents.data?.total_entries || empty.isPending}
            onClick={() => {
              if (window.confirm("Remove everything in the recycle bin for good? This cannot be undone.")) empty.mutate();
            }}
            className="ml-auto rounded-md border border-danger px-3 py-1 text-xs text-danger disabled:opacity-50"
          >
            {empty.isPending ? "Emptying…" : "Empty recycle bin"}
          </button>
        </div>
        {empty.isError && <p className="text-xs text-danger">The bin could not be emptied (administrators only).</p>}
        {empty.data && (
          <p className="text-xs text-ink-muted">
            Removed {empty.data.removed} files, {formatBytes(empty.data.freed_bytes)}
            {empty.data.failed > 0 ? `; ${empty.data.failed} could not be removed` : ""}.
          </p>
        )}
        {contents.data && contents.data.entries.length === 0 && <p className="text-xs text-ink-muted">The recycle bin is empty.</p>}
        {contents.data && contents.data.entries.length > 0 && (
          <ul className="flex flex-col divide-y divide-border-subtle text-xs">
            {contents.data.entries.map((entry) => (
              <li key={entry.id} className="flex flex-wrap items-baseline gap-x-3 py-1.5">
                <span className="min-w-0 flex-1 break-all" title={entry.original_path}>{entry.original_path}</span>
                <span className="text-ink-muted">{entry.reason.replace("_", " ")}</span>
                <span className="w-20 text-right tabular-nums">{formatBytes(entry.size_bytes)}</span>
                <span className="w-44 text-right text-ink-muted">
                  removed for good {new Date(entry.expires_at * 1000).toLocaleDateString()}
                </span>
              </li>
            ))}
          </ul>
        )}
        {contents.data && contents.data.total_entries > contents.data.entries.length && (
          <p className="text-xs text-ink-muted">Showing the newest {contents.data.entries.length}.</p>
        )}
      </section>
    </div>
  );
}
