import { Fragment, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Download, Globe, LoaderCircle } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { SKEW_TOLERANCE_MS, useServerClock } from "@/lib/useServerClock";
import { useFooterItems, type FooterItem } from "@/lib/interfacePreferences";
import { totalSpeed } from "@/lib/speedHistory";
import { describe, historyNotification, useNotifications, type Connection } from "@/lib/useNotifications";

import { formatBytes, queueQuery } from "./Queue";

/** The bottom bar: what just happened on the left, the chosen items on the right (Settings -> Web UI).
 *  Persistent rather than transient -- covering content with a toast is one of
 *  the complaints this project exists to answer. */
export function StatusBar() {
  const { recent, unread, connection, markRead } = useNotifications();
  const clock = useServerClock();
  const [open, setOpen] = useState(false);
  const [items] = useFooterItems();
  const showing = (item: FooterItem) => items.some((entry) => entry.item === item && entry.shown);
  const queue = useQuery({ ...queueQuery(), refetchInterval: 15_000, enabled: showing("queue") });

  // Shares useServerClock's own ["about"] query cache -- one request either way.
  const about = useQuery({
    queryKey: ["about"],
    queryFn: () => ok(api.GET("/about")),
    staleTime: Infinity,
  });

  // The address the outside world sees this host connect from, which is what
  // says whether traffic is leaving past a VPN. Rechecked on the server's own
  // interval, so this only has to read the cached answer.
  const egress = useQuery({
    queryKey: ["egress"],
    queryFn: () => ok(api.GET("/system/egress")),
    staleTime: 5 * 60_000,
    retry: false,
    enabled: showing("ip"),
  });

  // A fresh page has heard nothing live yet; the last download is still what just happened.
  const lastDownload = useQuery({
    queryKey: ["history", "latest"],
    queryFn: async () => {
      const { data, error } = await api.GET("/history", { params: { query: { limit: 1 } } });
      if (error) throw error;
      return data.items[0] ?? null;
    },
    staleTime: 60_000,
  });

  // What is being worked on now (2026-09-29); in memory on the server, so polling is cheap.
  const activity = useQuery({
    queryKey: ["activity"],
    queryFn: async () => {
      const { data, error } = await api.GET("/system/activity");
      if (error) throw error;
      return data.running;
    },
    refetchInterval: 3_000,
    retry: false,
  });
  const [jobsOpen, setJobsOpen] = useState(false);
  const running = activity.data ?? [];

  const latest = recent[0];
  const idle = lastDownload.data ? describe(historyNotification(lastDownload.data)) : "No downloads yet";
  const queueItems = queue.data?.items.length ?? 0;
  const speed = queue.data ? totalSpeed(queue.data.clients) : 0;
  const nodes: Record<FooterItem, React.ReactNode> = {
    events: (
      <button
        type="button"
        onClick={() => {
          setOpen(!open);
          markRead();
        }}
        disabled={recent.length === 0}
        aria-expanded={open}
        className="flex min-w-0 items-center gap-2 rounded-md px-1.5 py-0.5 text-ink-muted
                   hover:text-ink disabled:hover:text-ink-muted
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <Light connection={connection} />
        <span className="truncate">
          {latest ? describe(latest) : idle}
        </span>
        {unread > 0 && (
          <span className="shrink-0 rounded-full bg-accent px-1.5 py-px text-[0.6875rem]
                           font-medium text-accent-ink">
            {unread > 99 ? "99+" : unread}
          </span>
        )}
      </button>
    ),
    work: running[0] && (
      <button
        type="button"
        onClick={() => setJobsOpen(!jobsOpen)}
        aria-expanded={jobsOpen}
        title="Work running now"
        className="flex min-w-0 items-center gap-1.5 rounded-md px-1.5 py-0.5 text-ink-muted hover:text-ink
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <LoaderCircle aria-hidden className="size-3 shrink-0 animate-spin" />
        <span className="truncate">
          <RunningLine job={running[0]} />
        </span>
        {running.length > 1 && <span className="shrink-0">+{running.length - 1}</span>}
      </button>
    ),
    queue: queue.data && queue.data.clients.length > 0 && (
      <Link to="/activity/queue" title="Download queue" className="flex shrink-0 items-center gap-1.5 text-ink-muted hover:text-ink hover:underline">
        <Download aria-hidden className="size-3" />
        {queueItems === 0 ? "Queue idle" : `${queueItems} downloading${speed > 0 ? ` · ${formatBytes(speed)}/s` : ""}`}
      </Link>
    ),
    ip: egress.data?.public_ip && (
      <span
        className="hidden shrink-0 items-center gap-1.5 font-mono text-ink-muted sm:flex"
        title={
          `Address this host connects out from, via ${egress.data.source ?? "the configured check"}.` +
          (egress.data.error ? ` Last check failed: ${egress.data.error}` : "")
        }
      >
        <Globe aria-hidden className="size-3" />
        {egress.data.public_ip}
        {egress.data.error && <span className="text-warn">stale</span>}
      </span>
    ),
    time: clock.time && (
      <span className="flex shrink-0 items-center gap-3 font-mono tabular-nums text-ink-muted">
        {clock.skewMs !== null && Math.abs(clock.skewMs) > SKEW_TOLERANCE_MS && (
          <span className="font-sans text-warn" title="This browser's clock disagrees with the server's">
            clock skew {Math.round(clock.skewMs / 1000)}s
          </span>
        )}
        <span>
          {clock.time}
          <span className="ml-1.5 hidden text-ink-muted/70 sm:inline">{clock.timezone}</span>
        </span>
      </span>
    ),
    version: about.data?.version && (
      <a href="/about" className="shrink-0 font-mono text-ink-muted hover:text-ink hover:underline" title="About Solarr">
        v{about.data.version}
      </a>
    ),
  };
  // The bar's text runs left, the compact items right: the gap opens after the last shown event or work line.
  const shown = items.filter((entry) => entry.shown && nodes[entry.item]).map((entry) => entry.item);
  const lastWide = shown.reduce((last, item, at) => (item === "events" || item === "work" ? at : last), -1);

  return (
    <footer className="relative shrink-0 border-t border-border-subtle bg-surface">
      {open && recent.length > 0 && (
        <ul className="absolute bottom-full left-0 max-h-64 w-96 max-w-full overflow-y-auto border border-b-0
                       border-border-subtle bg-surface text-xs shadow-lg">
          {recent.map((event) => (
            <li
              key={event.id}
              className="flex items-baseline gap-2 border-b border-border-subtle px-3 py-1.5 last:border-b-0"
            >
              <span className="truncate">{describe(event)}</span>
              <time className="ml-auto shrink-0 font-mono text-ink-muted" dateTime={event.at}>
                {new Date(event.at).toLocaleTimeString(undefined, { hour12: false })}
              </time>
            </li>
          ))}
        </ul>
      )}

      {jobsOpen && running.length > 0 && (
        <ul className="absolute bottom-full left-0 w-[28rem] max-w-full border sm:left-1/3 border-b-0 border-border-subtle bg-surface
                       text-xs shadow-lg">
          {running.map((job) => (
            <li key={job.job_id} className="border-b border-border-subtle px-3 py-1.5 last:border-b-0">
              <RunningLine job={job} />
            </li>
          ))}
        </ul>
      )}

      <div className="flex items-center gap-3 px-4 py-1.5 text-xs">
        {shown.map((item, at) => (
          <Fragment key={item}>
            {nodes[item]}
            {at === lastWide && <span className="flex-1" />}
          </Fragment>
        ))}
      </div>
    </footer>
  );
}

/** A stream that has died silently is the failure worth showing: the interface
 *  keeps looking correct while going stale. */
function Light({ connection }: { connection: Connection }) {
  const tone =
    connection === "live"
      ? "bg-ok"
      : connection === "dropped"
        ? "bg-danger"
        : "bg-ink-muted";
  const label =
    connection === "live"
      ? "Live"
      : connection === "dropped"
        ? "Reconnecting"
        : "Connecting";
  return (
    <span
      title={`Event stream: ${label.toLowerCase()}`}
      className={`size-1.5 shrink-0 rounded-full ${tone}`}
    />
  );
}

type RunningJob = components["schemas"]["RunningJob"];

/** "Enriching metadata · 120 of 184 · about 2 min left": the count and the estimate where known. */
function RunningLine({ job }: { job: RunningJob }) {
  const parts = [job.name];
  if (job.total != null && job.total >= job.done) {
    parts.push(`${job.done.toLocaleString()} of ${job.total.toLocaleString()}`);
  } else if (job.done > 0) {
    parts.push(`${job.done.toLocaleString()} done`);
  }
  if (job.eta_seconds != null) parts.push(timeLeft(job.eta_seconds));
  return <>{parts.join(" · ")}</>;
}

function timeLeft(seconds: number): string {
  if (seconds < 60) return "under a minute left";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `about ${minutes} min left`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `about ${hours} h left` : `about ${hours} h ${rest} min left`;
}
