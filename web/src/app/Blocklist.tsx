import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

import { Card } from "./SettingsCard";

/** Section 10.11's failure memory, read and reversible. A release lands here
 *  when it fails; an automatic entry ages out on its own, a manual one stays
 *  until someone removes it. Removing one only allows it to be grabbed
 *  again -- it does not grab it. */
export function Blocklist() {
  const queryClient = useQueryClient();

  const entries = useQuery({
    queryKey: ["blocklist"],
    queryFn: () => ok(api.GET("/blocklist", { params: { query: { limit: 100 } } })),
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/blocklist/{id}", { params: { path: { id } } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["blocklist"] }),
  });

  return (
    <Card
      title="Failed releases"
      help="Releases that failed and will not be grabbed again. Removing an entry lets the next search choose it; it downloads nothing now."
    >
      {entries.isLoading && <Message>Loading…</Message>}
      {entries.error !== null && <Message>The blocklist could not be read.</Message>}
      {entries.data && entries.data.items.length === 0 && (
        <Message>Nothing is blocked. Failed releases land here as they happen.</Message>
      )}

      {entries.data && entries.data.items.length > 0 && (
        <ul className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
          {entries.data.items.map((entry) => (
            <li
              key={entry.id}
              className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
            >
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{entry.title}</div>
                <div className="truncate text-xs text-ink-muted">
                  {entry.reason}
                  {" · "}
                  {entry.source}
                  {entry.expires_at == null ? " · permanent" : ` · until ${formatDate(entry.expires_at)}`}
                </div>
              </div>
              <button
                type="button"
                disabled={remove.isPending}
                onClick={() => remove.mutate(entry.id)}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                           enabled:hover:text-ink disabled:opacity-50"
              >
                Allow again
              </button>
            </li>
          ))}
        </ul>
      )}

      {remove.isError && <p className="text-xs text-danger">That could not be removed.</p>}
    </Card>
  );
}

function formatDate(value: string): string {
  return new Date(value).toLocaleDateString();
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
