import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

const WINDOWS = [
  { label: "This month", back: 0, forward: 31 },
  { label: "±30 days", back: 30, forward: 30 },
  { label: "±90 days", back: 90, forward: 90 },
] as const;

/** Upcoming and recent releases by artists this library follows (revision
 *  7.4). Only works something wants or a monitored artist is credited on --
 *  a calendar of everything MusicBrainz knows would be noise. */
export function Calendar() {
  const [windowIndex, setWindowIndex] = useState(1);
  const chosen = WINDOWS[windowIndex] ?? WINDOWS[1]!;
  const { from, to } = windowDates(chosen.back, chosen.forward);

  const entries = useQuery({
    queryKey: ["calendar", from, to],
    queryFn: async () => {
      const { data, error } = await api.GET("/calendar", {
        params: { query: { from, to, limit: 300 } },
      });
      if (error) throw error;
      return data;
    },
  });

  const byDate = new Map<string, NonNullable<typeof entries.data>>();
  for (const entry of entries.data ?? []) {
    const existing = byDate.get(entry.release_date);
    if (existing) existing.push(entry);
    else byDate.set(entry.release_date, [entry]);
  }

  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Calendar</h2>
        <div className="flex gap-1">
          {WINDOWS.map((option, index) => (
            <button
              key={option.label}
              type="button"
              aria-pressed={windowIndex === index}
              onClick={() => setWindowIndex(index)}
              className={`rounded-md border px-2 py-1 text-xs ${
                windowIndex === index
                  ? "border-accent bg-accent text-accent-ink"
                  : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>
      <p className="text-xs text-ink-muted">
        Releases by artists you follow, and albums you have asked for. Dates come from the
        metadata provider and are sometimes only a month or a year.
      </p>

      {entries.isLoading && <Message>Loading…</Message>}
      {entries.error !== null && <Message>The calendar could not be read.</Message>}
      {entries.data && entries.data.length === 0 && (
        <Message>
          Nothing in this window. Monitor an artist, or want an album, and its releases appear here.
        </Message>
      )}

      {byDate.size > 0 && (
        <ul className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto">
          {[...byDate.entries()].map(([date, releases]) => (
            <li key={date} className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink-muted">{formatDate(date)}</span>
              <ul className="flex flex-col gap-1">
                {releases.map((release) => (
                  <li
                    key={release.work_id}
                    className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
                  >
                    <Link
                      to="/works/$id"
                      params={{ id: String(release.work_id) }}
                      className="min-w-0 flex-1 truncate hover:underline"
                    >
                      {release.title}
                    </Link>
                    {release.artist != null && release.artist !== "" && (
                      <span className="shrink-0 truncate text-xs text-ink-muted">{release.artist}</span>
                    )}
                    {release.primary_type != null && (
                      <span className="shrink-0 text-xs text-ink-muted">{release.primary_type}</span>
                    )}
                    <span
                      className={`shrink-0 text-xs ${release.on_disk ? "text-ink-muted" : "text-accent"}`}
                    >
                      {release.on_disk ? "have it" : "wanted"}
                    </span>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** A partial date is shown as given: "2024-03" means the month is all the
 *  provider knows, and inventing a day would be a claim nobody made. */
function formatDate(date: string): string {
  if (date.length === 10) return new Date(date).toLocaleDateString();
  return date;
}

function windowDates(back: number, forward: number): { from: string; to: string } {
  const now = new Date();
  const start = new Date(now);
  start.setDate(start.getDate() - back);
  const end = new Date(now);
  end.setDate(end.getDate() + forward);
  return { from: start.toISOString().slice(0, 10), to: end.toISOString().slice(0, 10) };
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
