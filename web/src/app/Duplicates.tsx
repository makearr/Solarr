import { useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

import { formatBytes } from "./Queue";

/** Files of this library that look like copies of one another: the same size and the same sampled content, wherever
 *  they sit. A report only: nothing is linked or removed from here yet. */
export function Duplicates() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const report = useQuery({
    queryKey: ["duplicates", categoryId],
    enabled: categoryId !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/duplicates", { params: { path: { id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const data = report.data;
  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Duplicates</h1>
        <p className="text-sm text-ink-muted">
          Files of the same size whose sampled content is the same, however they are named. A hardlinked file is one file with
          two paths and is not a duplicate. This only looks: nothing is linked or removed.
        </p>
      </header>
      {report.isLoading && <p className="text-sm text-ink-muted">Looking…</p>}
      {report.error !== null && !report.isLoading && <p className="text-sm text-danger">The report could not be made.</p>}
      {data && data.groups.length === 0 && <p className="text-sm text-ink-muted">No duplicate files found.</p>}
      {data && data.groups.length > 0 && (
        <>
          <p className="text-sm">
            {data.groups.length} {data.groups.length === 1 ? "group" : "groups"}; keeping one copy of each would free{" "}
            <span className="font-medium">{formatBytes(data.reclaimable_bytes)}</span>.
          </p>
          <ul className="flex flex-col gap-2">
            {data.groups.map((group, index) => (
              <li key={index} className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
                <div className="flex items-baseline gap-3 text-xs text-ink-muted">
                  <span className="text-sm font-medium text-ink">{formatBytes(group.size_bytes)} each</span>
                  <span>{group.copies.length} copies</span>
                  <span>frees {formatBytes(group.reclaimable_bytes)}</span>
                </div>
                <ul className="flex flex-col gap-0.5 font-mono text-xs">
                  {group.copies.map((copy) => (
                    <li key={copy.media_object_id} className="break-all">
                      {copy.paths.join("  =  ")}
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
