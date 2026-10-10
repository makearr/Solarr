import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { useIsAdministrator } from "@/lib/useAdministrator";
import type { components } from "@/api/schema";

type QueueItem = components["schemas"]["QueueItem"];
type QueueClient = components["schemas"]["QueueClient"];
type HeldRelease = components["schemas"]["HeldRelease"];

// Read live from the clients, so refreshed as Lidarr's queue is rather than on events alone.
const REFRESH_MS = 5_000;

/** Activity → Queue, as in Lidarr: what is downloading now, and each client's own state. */
export function Queue() {
  const queue = useQueue();

  return (
    <div className="flex h-full flex-col overflow-y-auto">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Queue</h2>
      </div>
      <div className="flex flex-col gap-4 p-4">
        {queue.isLoading && <Message>Loading the queue…</Message>}
        {queue.error !== null && <Message>The queue could not be read.</Message>}
        {queue.data && (
          <>
            <ClientList clients={queue.data.clients} items={queue.data.items} />
            {queue.data.items.length === 0 ? (
              <Message>Nothing is downloading. What is sent to a download client appears here until imported.</Message>
            ) : (
              <QueueTable items={queue.data.items} />
            )}
            <HeldList held={queue.data.held} />
          </>
        )}
      </div>
    </div>
  );
}

/** The queue, or one work's part of it. */
export function useQueue(workId?: number) {
  return useQuery({
    queryKey: ["queue", workId],
    queryFn: async () => {
      const { data, error } = await api.GET("/queue", { params: { query: { work_id: workId } } });
      if (error) throw error;
      return data;
    },
    refetchInterval: REFRESH_MS,
  });
}

function ClientList({ clients, items }: { clients: readonly QueueClient[]; items: readonly QueueItem[] }) {
  if (clients.length === 0) {
    return <Message>No download client is enabled, so nothing can be downloaded.</Message>;
  }
  return (
    <ul className="flex flex-wrap gap-2 text-xs">
      {clients.map((client) => (
        <li key={client.id} className="flex items-center gap-2 rounded-md border border-border-subtle bg-surface px-3 py-2">
          <span className="font-medium text-ink">{client.name}</span>
          {client.error ? (
            <span className="text-danger" title={client.error}>
              not reachable
            </span>
          ) : client.paused ? (
            <span className="text-warn">paused -- nothing downloads until it is resumed in the client</span>
          ) : (
            <span className="text-ink-muted">
              {client.speed_bytes_per_sec > 0 ? `${formatBytes(client.speed_bytes_per_sec)}/s` : "idle"} ·{" "}
              {client.items} in its queue · {formatBytes(client.size_left_bytes)} left
            </span>
          )}
          <PauseMine client={client} items={items.filter((item) => item.client === client.name)} />
        </li>
      ))}
    </ul>
  );
}

/** Pauses or resumes Solarr's own downloads in one client, never the client: other apps share it. */
function PauseMine({ client, items }: { client: QueueClient; items: readonly QueueItem[] }) {
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const waiting = items.filter((item) => item.state === "QUEUED" || item.state === "DOWNLOADING");
  const allPaused = waiting.length > 0 && waiting.every((item) => item.client_state === "paused");
  const toggle = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/queue/clients/{id}/{action}", {
        params: { path: { id: client.id, action: allPaused ? "resume" : "pause" } },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["queue"] }),
  });
  if (!administrator || waiting.length === 0) return null;
  return (
    <button
      type="button"
      disabled={toggle.isPending}
      onClick={() => toggle.mutate()}
      title="Only Solarr's downloads; anything else in the client carries on"
      className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink disabled:opacity-50"
    >
      {allPaused ? "Resume Solarr's downloads" : "Pause Solarr's downloads"}
    </button>
  );
}

function QueueTable({ items }: { items: readonly QueueItem[] }) {
  return (
    <table className="w-full text-left text-xs">
      <thead className="text-ink-muted">
        <tr className="border-b border-border-subtle">
          <th className="px-2 py-1.5 font-medium">Title</th>
          <th className="px-2 py-1.5 font-medium">Release</th>
          <th className="px-2 py-1.5 font-medium">Status</th>
          <th className="px-2 py-1.5 font-medium">Progress</th>
          <th className="px-2 py-1.5 font-medium">Time left</th>
          <th className="px-2 py-1.5 font-medium">From → to</th>
          <th className="px-2 py-1.5" />
        </tr>
      </thead>
      <tbody>
        {items.map((item) => (
          <QueueRow key={item.id} item={item} />
        ))}
      </tbody>
    </table>
  );
}

function QueueRow({ item }: { item: QueueItem }) {
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [blocklist, setBlocklist] = useState(false);
  const remove = useMutation({
    mutationFn: async () => {
      const { error } = await api.DELETE("/queue/{id}", {
        params: { path: { id: item.id }, query: { blocklist } },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setConfirming(false);
      void queryClient.invalidateQueries({ queryKey: ["queue"] });
      void queryClient.invalidateQueries({ queryKey: ["history"] });
    },
  });
  // An import held for a person moves nothing; only that kind of importing item carries a note.
  const removable = item.state !== "IMPORTING" || Boolean(item.error);

  return (
    <tr className="border-b border-border-subtle align-top">
      <td className="px-2 py-2">
        {item.work_id != null ? (
          <Link to="/works/$id" params={{ id: String(item.work_id) }} className="text-ink hover:underline">
            {[item.artist, item.album].filter(Boolean).join(" — ") || "Untitled"}
          </Link>
        ) : (
          (item.album ?? "")
        )}
      </td>
      <td className="max-w-80 px-2 py-2">
        <span className="block truncate" title={item.release_title}>
          {item.release_title}
        </span>
        {item.error && (
          <span className="block text-danger">
            {item.error}
            {item.state === "IMPORTING" && (
              <>
                {" "}
                <Link to="/storage/import" className="underline">
                  Review
                </Link>
              </>
            )}
          </span>
        )}
      </td>
      <td className="px-2 py-2">{statusLabel(item)}</td>
      <td className="w-40 px-2 py-2">
        <ProgressBar item={item} />
      </td>
      <td className="px-2 py-2 text-ink-muted">{item.eta_seconds != null ? formatDuration(item.eta_seconds) : ""}</td>
      <td className="px-2 py-2 text-ink-muted">{[item.indexer, item.client].filter(Boolean).join(" → ")}</td>
      <td className="px-2 py-2 text-right">
        {confirming ? (
          <span className="flex flex-col items-end gap-1">
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked={blocklist} onChange={(event) => setBlocklist(event.target.checked)} />
              Blocklist this release and search for another
            </label>
            <span className="flex gap-1.5">
              <button
                type="button"
                disabled={remove.isPending}
                onClick={() => remove.mutate()}
                className="rounded-md bg-danger px-2 py-0.5 text-accent-ink disabled:opacity-50"
              >
                Remove
              </button>
              <button
                type="button"
                onClick={() => setConfirming(false)}
                className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink"
              >
                Cancel
              </button>
            </span>
            {remove.isError && <span className="text-danger">It could not be removed.</span>}
          </span>
        ) : (
          removable && (
            <button
              type="button"
              onClick={() => setConfirming(true)}
              title="Remove from the download client, with its files"
              className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-danger"
            >
              Remove
            </button>
          )
        )}
      </td>
    </tr>
  );
}

const ORIGIN_LABELS: Record<HeldRelease["origin"], string> = { user: "You", rss: "RSS", automatic: "Automatic" };

/** ADR-57's HELD: found and chosen, waiting because the client already has as many as it may. */
function HeldList({ held }: { held: readonly HeldRelease[] }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">
        Waiting for room in the download client ({held.length})
      </h3>
      <p className="text-xs text-ink-muted">
        Releases found while the client already had as many as its &ldquo;Downloads at once&rdquo;. They are sent
        in this order as downloads finish: your own grabs first, then RSS, then new releases, then older ones.
      </p>
      {held.length === 0 ? (
        <Message>Nothing is waiting.</Message>
      ) : (
        <table className="w-full text-left text-xs">
          <thead className="text-ink-muted">
            <tr className="border-b border-border-subtle">
              <th className="w-8 px-2 py-1.5 text-right font-medium">#</th>
              <th className="px-2 py-1.5 font-medium">Title</th>
              <th className="px-2 py-1.5 font-medium">Release</th>
              <th className="px-2 py-1.5 font-medium">Size</th>
              <th className="px-2 py-1.5 font-medium">Asked by</th>
              <th className="px-2 py-1.5 font-medium">Indexer</th>
              <th className="px-2 py-1.5 font-medium">Waiting since</th>
            </tr>
          </thead>
          <tbody>
            {held.map((release, index) => (
              <tr key={`${release.work_id ?? 0}-${release.release_title}`} className="border-b border-border-subtle">
                <td className="px-2 py-1.5 text-right text-ink-muted">{index + 1}</td>
                <td className="px-2 py-1.5">
                  {release.work_id != null ? (
                    <Link to="/works/$id" params={{ id: String(release.work_id) }} className="text-ink hover:underline">
                      {[release.artist, release.album].filter(Boolean).join(" — ") || "Untitled"}
                    </Link>
                  ) : (
                    (release.album ?? "")
                  )}
                </td>
                <td className="max-w-80 px-2 py-1.5">
                  <span className="block truncate text-ink-muted" title={release.release_title}>
                    {release.release_title}
                  </span>
                </td>
                <td className="px-2 py-1.5 text-ink-muted">
                  {release.size_bytes != null ? formatBytes(release.size_bytes) : ""}
                </td>
                <td className="px-2 py-1.5">{ORIGIN_LABELS[release.origin]}</td>
                <td className="px-2 py-1.5 text-ink-muted">{release.indexer ?? ""}</td>
                <td className="px-2 py-1.5 text-ink-muted">{new Date(release.held_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function ProgressBar({ item }: { item: QueueItem }) {
  if (!item.size_bytes) return null;
  const done = Math.max(0, Math.min(1, 1 - (item.size_left_bytes ?? 0) / item.size_bytes));
  return (
    <div className="flex items-center gap-2" title={`${formatBytes(item.size_bytes)}`}>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-border-subtle">
        <div className="h-full bg-accent" style={{ width: `${Math.round(done * 100)}%` }} />
      </div>
      <span className="w-9 text-right text-ink-muted">{Math.round(done * 100)}%</span>
    </div>
  );
}

/** Solarr's state, sharpened by the client's own where it says more. */
export function statusLabel(item: QueueItem): string {
  switch (item.state) {
    case "IMPORTING":
      return "Importing";
    case "COMPLETED":
      return item.error ? "Downloaded, waiting" : "Downloaded";
  }
  switch (item.client_state) {
    case "paused":
      return "Paused";
    case "downloading":
      return "Downloading";
    case "processing":
      return "Unpacking";
    case "completed":
      return "Downloaded";
    case "failed":
      return "Failed";
    default:
      return "Queued";
  }
}

export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
}

export function formatDuration(seconds: number): string {
  if (seconds < 60) return "under a minute";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min`;
  return `${Math.floor(minutes / 60)} h ${minutes % 60} min`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
