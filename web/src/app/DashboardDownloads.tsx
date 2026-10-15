import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";

import { appendSample, sparkPath, totalSpeed, type SpeedSample } from "@/lib/speedHistory";

import { formatBytes, formatDuration, ProgressBar, statusLabel, useQueue } from "./Queue";

/** How many downloads the dashboard lists before "All downloads". */
const SHOWN = 6;

/** The download clients' speed over the last minutes and what is downloading, as fillarr's dashboard shows it; the
 *  graph is drawn from the readings taken while the page is open. */
export function DashboardDownloads() {
  const queue = useQueue();
  const [samples, setSamples] = useState<SpeedSample[]>([]);
  const clients = queue.data?.clients;
  const updatedAt = queue.dataUpdatedAt;

  useEffect(() => {
    if (clients === undefined || clients.length === 0) return;
    setSamples((previous) => appendSample(previous, { at: updatedAt, bytesPerSec: totalSpeed(clients) }));
  }, [clients, updatedAt]);

  if (!queue.data) return null;
  const { items } = queue.data;
  if (queue.data.clients.length === 0) {
    return (
      <p className="text-sm text-ink-muted">
        No download client yet.{" "}
        <Link to="/connections/downloaders" className="text-accent underline underline-offset-2">
          Add one
        </Link>{" "}
        to download what is missing.
      </p>
    );
  }
  const speed = totalSpeed(queue.data.clients);
  const ordered = [...items].sort((a, b) => rank(a.client_state) - rank(b.client_state));

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col">
          <span className="text-2xl font-semibold tabular-nums">{speed > 0 ? `${formatBytes(speed)}/s` : "Idle"}</span>
          <span className="text-xs text-ink-muted">
            {items.length === 0 ? "Nothing of Solarr's is downloading" : `${items.length} of Solarr's downloads in flight`}
          </span>
        </div>
        <SpeedGraph samples={samples} />
      </div>

      <ul className="flex flex-col gap-1 text-xs text-ink-muted">
        {queue.data.clients.map((client) => (
          <li key={client.id} className="flex flex-wrap gap-x-2">
            <span className="text-ink">{client.name}</span>
            {client.error ? (
              <span className="text-danger">{client.error}</span>
            ) : (
              <span>
                {client.paused ? "paused" : client.speed_bytes_per_sec > 0 ? `${formatBytes(client.speed_bytes_per_sec)}/s` : "idle"} ·{" "}
                {client.items} in its queue · {formatBytes(client.size_left_bytes)} left
              </span>
            )}
          </li>
        ))}
      </ul>

      {ordered.length > 0 && (
        <ul className="flex flex-col divide-y divide-border-subtle border-t border-border-subtle">
          {ordered.slice(0, SHOWN).map((item) => (
            <li key={item.id} className="grid grid-cols-[1fr_9rem] items-center gap-x-4 gap-y-1 py-2 text-xs sm:grid-cols-[1fr_7rem_9rem]">
              <div className="min-w-0">
                <div className="truncate text-sm text-ink">{item.album ?? item.release_title}</div>
                <div className="truncate text-ink-muted">{item.artist ?? item.release_title}</div>
              </div>
              <span className="hidden text-ink-muted sm:block">
                {statusLabel(item)}
                {item.eta_seconds != null ? ` · ${formatDuration(item.eta_seconds)}` : ""}
              </span>
              <ProgressBar item={item} />
            </li>
          ))}
        </ul>
      )}
      <Link to="/activity/queue" className="self-start text-xs text-ink-muted underline hover:text-ink">
        {ordered.length > SHOWN ? `All ${ordered.length} downloads` : "Open the queue"}
      </Link>
    </div>
  );
}

/** Downloading first, then waiting, then the rest. */
function rank(state: string | undefined): number {
  switch (state) {
    case "downloading":
      return 0;
    case "queued":
      return 1;
    case "paused":
      return 2;
    default:
      return 3;
  }
}

const GRAPH_WIDTH = 240;
const GRAPH_HEIGHT = 40;

function SpeedGraph({ samples }: { samples: readonly SpeedSample[] }) {
  const path = sparkPath(samples, GRAPH_WIDTH, GRAPH_HEIGHT);
  const peak = Math.max(0, ...samples.map((sample) => sample.bytesPerSec));
  return (
    <figure className="flex flex-col items-end gap-0.5">
      <svg
        width={GRAPH_WIDTH}
        height={GRAPH_HEIGHT}
        viewBox={`0 0 ${GRAPH_WIDTH} ${GRAPH_HEIGHT}`}
        role="img"
        aria-label="Download speed over the last minutes"
        className="rounded border border-border-subtle bg-canvas"
      >
        {path && <path d={path} className="fill-accent/30 stroke-accent" strokeWidth={1} />}
      </svg>
      <figcaption className="text-[10px] text-ink-muted">
        {samples.length < 2 ? "Speed, measured while this page is open" : `Last ${minutesOf(samples)} · peak ${formatBytes(peak)}/s`}
      </figcaption>
    </figure>
  );
}

function minutesOf(samples: readonly SpeedSample[]): string {
  const span = (samples.at(-1)?.at ?? 0) - (samples.at(0)?.at ?? 0);
  const minutes = Math.max(1, Math.round(span / 60_000));
  return minutes === 1 ? "minute" : `${minutes} minutes`;
}
