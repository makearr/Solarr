import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

/** ADR-15's backups, read from disk. Restoring is deliberately absent: it is
 *  the documented offline procedure, and a restore button that half-works is
 *  worse than none. */
export function Backups() {
  const queryClient = useQueryClient();

  const files = useQuery({
    queryKey: ["backups"],
    queryFn: async () => {
      const { data, error } = await api.GET("/system/backups");
      if (error) throw error;
      return data;
    },
  });

  const runNow = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/system/backups", {});
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["backups"] }),
  });

  return (
    <div className="flex h-full flex-col gap-4 p-4">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Backups</h2>
        <button
          type="button"
          disabled={runNow.isPending}
          onClick={() => runNow.mutate()}
          className="rounded-md border border-border-subtle px-3 py-1 text-xs text-ink-muted
                     enabled:hover:text-ink disabled:opacity-50"
        >
          {runNow.isPending ? "Backing up…" : "Back up now"}
        </button>
      </div>
      <p className="max-w-2xl text-xs text-ink-muted">
        Restoring is an offline procedure: stop the server and put the file back. There is no
        restore button on purpose.
      </p>

      {files.isLoading && <Message>Loading…</Message>}
      {files.error !== null && <Message>The backup directory could not be read.</Message>}
      {files.data && files.data.length === 0 && <Message>No backups yet.</Message>}

      {files.data && files.data.length > 0 && (
        <ul className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
          {files.data.map((file) => (
            <li
              key={file.name}
              className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
            >
              <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.name}</span>
              {file.pre_migration === true && (
                <span className="shrink-0 text-xs text-ink-muted">before a migration</span>
              )}
              <span className="shrink-0 text-xs text-ink-muted">{formatSize(file.size_bytes)}</span>
              <span className="shrink-0 text-xs text-ink-muted">
                {new Date(file.taken_at).toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      )}

      {runNow.isError && <p className="text-xs text-danger">The backup did not complete.</p>}
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
