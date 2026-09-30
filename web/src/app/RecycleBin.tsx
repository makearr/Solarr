import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

import { inputStyle } from "./LibraryNaming";

const GB = 1024 * 1024 * 1024;

/** The recycle bin's space bound, for every library (asked 2026-09-30); each library keeps its own
 *  retention in days under its File Management. */
export function RecycleBin() {
  const queryClient = useQueryClient();
  const limits = useQuery({
    queryKey: ["recycleBinLimits"],
    queryFn: async () => {
      const { data, error } = await api.GET("/recyclebin");
      if (error) throw error;
      return data;
    },
  });
  const [maxGB, setMaxGB] = useState(0);
  const [percent, setPercent] = useState(0);
  useEffect(() => {
    if (!limits.data) return;
    setMaxGB(Math.round(limits.data.max_bytes / GB));
    setPercent(limits.data.max_percent_of_root);
  }, [limits.data]);

  const save = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.PATCH("/recyclebin", {
        body: { max_bytes: maxGB * GB, max_percent_of_root: percent },
      });
      if (error) throw error;
      return data;
    },
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
        <button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate()}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        {save.isError && <span className="text-xs text-danger">That was not saved -- the limits have a problem.</span>}
        {save.isSuccess && !dirty && <span className="text-xs text-ink-muted">Saved; the next cleanup applies it.</span>}
      </div>
    </div>
  );
}
