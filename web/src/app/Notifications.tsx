import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, Bell, CheckCircle2, XCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";

import { pageTier } from "./sections";

const SEEN_KEY = "solarr.notifications.seen";
const SEEN_EVENT = "solarr:notifications-seen";
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

function readSeen(): number {
  try {
    return Number(localStorage.getItem(SEEN_KEY)) || 0;
  } catch {
    return 0;
  }
}

/** What needs attention: the instance's standing warnings (D9), and the last week's failed downloads and
 *  imports, newer than what this browser has seen. The warnings go away when their cause does. */
function useNotificationCentre() {
  const tier = useTier();
  const health = useQuery({
    queryKey: ["system-health"],
    queryFn: () => ok(api.GET("/system/health")),
    refetchInterval: 60_000,
  });
  const history = useQuery({
    queryKey: ["history", "notifications"],
    queryFn: async () => {
      const { data, error } = await api.GET("/history", { params: { query: { limit: 100 } } });
      if (error) throw error;
      return data.items;
    },
    refetchInterval: 60_000,
  });
  const warnings = (health.data ?? []).filter((check) => check.link == null || isTierVisible(tier, pageTier(check.link)));
  const cutoff = Date.now() - WEEK_MS;
  const events = (history.data ?? []).filter((item) => item.kind !== "grabbed" && new Date(item.at).getTime() >= cutoff);
  return { warnings, events, loading: health.isLoading || history.isLoading };
}

/** The header's bell: a count of standing warnings plus failures this browser has not looked at yet. */
export function NotificationBell() {
  const { warnings, events } = useNotificationCentre();
  const [seen, setSeen] = useState(readSeen);
  useEffect(() => {
    const update = () => setSeen(readSeen());
    window.addEventListener(SEEN_EVENT, update);
    window.addEventListener("storage", update);
    return () => {
      window.removeEventListener(SEEN_EVENT, update);
      window.removeEventListener("storage", update);
    };
  }, []);
  const fresh = events.filter((item) => item.kind === "failed" && new Date(item.at).getTime() > seen).length;
  const count = warnings.length + fresh;
  return (
    <Link
      to="/activity/notifications"
      aria-label={count > 0 ? `Notifications, ${count} need attention` : "Notifications"}
      className="relative rounded-md p-1.5 text-ink-muted hover:text-ink"
    >
      <Bell aria-hidden className="size-4" />
      {count > 0 && (
        <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-danger px-1 text-center text-[0.625rem] leading-4 text-accent-ink">
          {count}
        </span>
      )}
    </Link>
  );
}

export function Notifications() {
  const { warnings, events, loading } = useNotificationCentre();
  const [seen] = useState(readSeen);
  // Opening the centre is looking at it: the bell clears, the list keeps what it showed.
  useEffect(() => {
    const timer = window.setTimeout(() => {
      try {
        localStorage.setItem(SEEN_KEY, String(Date.now()));
        window.dispatchEvent(new Event(SEEN_EVENT));
      } catch {
        // A per-browser convenience; losing it costs a badge that stays.
      }
    }, 1500);
    return () => window.clearTimeout(timer);
  }, []);

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-6 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Notifications</h1>
        <p className="text-sm text-ink-muted">
          What needs your attention: warnings that stay until their cause is fixed, and what failed in the last week.
        </p>
      </header>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-medium">Warnings</h2>
        {loading && <p className="text-sm text-ink-muted">Loading…</p>}
        {!loading && warnings.length === 0 && (
          <p className="flex items-center gap-2 text-sm text-ink-muted">
            <CheckCircle2 aria-hidden className="size-4 text-ok" />
            Nothing is wrong right now.
          </p>
        )}
        <ul className="flex flex-col gap-1">
          {warnings.map((check) => (
            <li
              key={`${check.id}:${check.message}`}
              className="flex items-start gap-2 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
            >
              {check.severity === "error" ? (
                <XCircle aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
              ) : (
                <AlertTriangle aria-hidden className="mt-0.5 size-4 shrink-0 text-ink-muted" />
              )}
              <span className="flex-1">{check.message}</span>
              {check.link != null && (
                <Link to={check.link} className="shrink-0 text-xs text-accent hover:underline">
                  Fix
                </Link>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-medium">Last 7 days</h2>
        {!loading && events.length === 0 && <p className="text-sm text-ink-muted">No failures or imports.</p>}
        <ul className="flex flex-col gap-1">
          {events.map((item) => (
            <li
              key={item.id}
              className={`flex items-start gap-2 rounded-md border bg-surface px-3 py-2 text-sm ${
                item.kind === "failed" && new Date(item.at).getTime() > seen ? "border-danger" : "border-border-subtle"
              }`}
            >
              {item.kind === "failed" ? (
                <XCircle aria-hidden className="mt-0.5 size-4 shrink-0 text-danger" />
              ) : (
                <CheckCircle2 aria-hidden className="mt-0.5 size-4 shrink-0 text-ok" />
              )}
              <span className="min-w-0 flex-1">
                <span className="block truncate">
                  {item.kind === "failed" ? "Download failed" : item.kind === "imported" ? "Imported" : item.kind}:{" "}
                  {item.work_id != null ? (
                    <Link to="/works/$id" params={{ id: String(item.work_id) }} className="hover:underline">
                      {item.album ?? item.release_title}
                    </Link>
                  ) : (
                    (item.album ?? item.release_title)
                  )}
                  {item.artist ? ` - ${item.artist}` : ""}
                </span>
                {item.detail && <span className="block truncate text-xs text-ink-muted">{item.detail}</span>}
              </span>
              <span className="shrink-0 text-xs text-ink-muted">{new Date(item.at).toLocaleString()}</span>
            </li>
          ))}
        </ul>
        <Link to="/activity/history" className="w-fit text-xs text-accent hover:underline">
          All history
        </Link>
      </section>
    </div>
  );
}
