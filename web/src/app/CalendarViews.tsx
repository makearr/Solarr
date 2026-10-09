import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { CoverArt } from "./CoverArt";

type CalendarEntry = components["schemas"]["CalendarEntry"];

/** A local date as YYYY-MM-DD; toISOString would shift it by the zone. */
export function isoDate(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

export function parseIso(value: string | undefined): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value ?? "");
  return match ? new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])) : new Date();
}

export function addDays(date: Date, days: number): Date {
  const next = new Date(date);
  next.setDate(next.getDate() + days);
  return next;
}

/** The Monday on or before a date: weeks read Monday-first, as a European calendar does. */
export function weekStart(date: Date): Date {
  return addDays(date, -((date.getDay() + 6) % 7));
}

const WEEKDAY_NAMES = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"] as const;

function useReleases(from: string, to: string) {
  return useQuery({
    queryKey: ["calendar", from, to],
    queryFn: async () => {
      const { data, error } = await api.GET("/calendar", { params: { query: { from, to, limit: 500 } } });
      if (error) throw error;
      return data;
    },
  });
}

function byDate(entries: readonly CalendarEntry[] | undefined): Map<string, CalendarEntry[]> {
  const grouped = new Map<string, CalendarEntry[]>();
  for (const entry of entries ?? []) {
    const existing = grouped.get(entry.release_date);
    if (existing) existing.push(entry);
    else grouped.set(entry.release_date, [entry]);
  }
  return grouped;
}

/** An episode opens its series page, a release its album page. */
export function entryTarget(entry: CalendarEntry) {
  if (entry.part_id != null && entry.category_id != null) {
    return {
      to: "/library/$categoryId/works/$workId",
      params: { categoryId: String(entry.category_id), workId: String(entry.work_id) },
    } as const;
  }
  return { to: "/works/$id", params: { id: String(entry.work_id) } } as const;
}

/** Several episodes of one series share a work and a day, so the part is part of the key. */
export function entryKey(entry: CalendarEntry): string {
  return `${entry.work_id}:${entry.part_id ?? 0}`;
}

// Lidarr's colours: have it, wanted and coming, wanted and out.
function tone(entry: CalendarEntry, today: string): string {
  if (entry.on_disk) return "border-l-ok";
  return entry.release_date > today ? "border-l-accent" : "border-l-danger";
}

/** One release: cover, title and artist, coloured by whether it is held. */
function ReleaseCard({ entry, today, compact }: { entry: CalendarEntry; today: string; compact?: boolean }) {
  const target = entryTarget(entry);
  return (
    <Link
      {...target}
      title={`${entry.title}${entry.artist ? ` - ${entry.artist}` : ""} (${entry.on_disk ? "have it" : "wanted"})`}
      className={`flex items-center gap-2 rounded border border-l-4 border-border-subtle bg-surface hover:bg-canvas ${tone(entry, today)} ${
        compact ? "px-1.5 py-0.5 text-[0.6875rem]" : "p-2 text-xs"
      }`}
    >
      {!compact && entry.part_id == null && <CoverArt workId={entry.work_id} title={entry.title} size="thumb" className="size-10 shrink-0 rounded object-cover" />}
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{entry.title}</span>
        {!compact && entry.artist && <span className="block truncate text-ink-muted">{entry.artist}</span>}
      </span>
    </Link>
  );
}

/** Releases whose provider date is only a month or a year have no day to sit on. */
function UndatedStrip({ entries, today }: { entries: readonly CalendarEntry[]; today: string }) {
  const partial = entries.filter((entry) => entry.release_date.length !== 10);
  if (partial.length === 0) return null;
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium text-ink-muted">Dated only by month or year</h3>
      <div className="grid gap-1.5 sm:grid-cols-2 lg:grid-cols-3">
        {partial.map((entry) => (
          <ReleaseCard key={entryKey(entry)} entry={entry} today={today} />
        ))}
      </div>
    </section>
  );
}

export function MonthView({ anchor, today }: { anchor: Date; today: string }) {
  const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1);
  const gridStart = weekStart(first);
  const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
  const weeks = Math.ceil(((weekStart(last).getTime() - gridStart.getTime()) / 86_400_000 + 7) / 7);
  const gridEnd = addDays(gridStart, weeks * 7 - 1);
  const releases = useReleases(isoDate(gridStart), isoDate(gridEnd));
  const grouped = byDate(releases.data);
  const monthPrefix = isoDate(first).slice(0, 7);
  const partial = (releases.data ?? []).filter(
    (entry) => entry.release_date.length === 7 && entry.release_date === monthPrefix,
  );

  return (
    <>
      <div className="grid grid-cols-7 gap-px overflow-hidden rounded-lg border border-border-subtle bg-border-subtle text-xs">
        {WEEKDAY_NAMES.map((name) => (
          <span key={name} className="bg-surface px-2 py-1 font-medium text-ink-muted">
            {name}
          </span>
        ))}
        {Array.from({ length: weeks * 7 }, (_, index) => {
          const date = addDays(gridStart, index);
          const iso = isoDate(date);
          const entries = grouped.get(iso) ?? [];
          const inMonth = date.getMonth() === anchor.getMonth();
          return (
            <div key={iso} className={`flex min-h-24 flex-col gap-1 p-1 ${inMonth ? "bg-canvas" : "bg-surface"}`}>
              <Link
                to="/calendar"
                search={{ view: "day", date: iso }}
                className={`w-fit rounded px-1 tabular-nums ${iso === today ? "bg-accent text-accent-ink" : inMonth ? "text-ink" : "text-ink-muted"}`}
              >
                {date.getDate()}
              </Link>
              {entries.slice(0, 3).map((entry) => (
                <ReleaseCard key={entryKey(entry)} entry={entry} today={today} compact />
              ))}
              {entries.length > 3 && (
                <Link to="/calendar" search={{ view: "day", date: iso }} className="px-1 text-[0.6875rem] text-ink-muted hover:text-ink">
                  +{entries.length - 3} more
                </Link>
              )}
            </div>
          );
        })}
      </div>
      {releases.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {releases.error !== null && <p className="text-sm text-danger">The calendar could not be read.</p>}
      <UndatedStrip entries={partial} today={today} />
    </>
  );
}

/** Seven columns from a start date: the week view and Lidarr's forecast. */
export function DaysView({ start, days, today }: { start: Date; days: number; today: string }) {
  const releases = useReleases(isoDate(start), isoDate(addDays(start, days - 1)));
  const grouped = byDate(releases.data);
  return (
    <>
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4 xl:grid-cols-7">
        {Array.from({ length: days }, (_, index) => {
          const date = addDays(start, index);
          const iso = isoDate(date);
          const entries = grouped.get(iso) ?? [];
          const relative = iso === today ? "Today" : iso === isoDate(addDays(parseIso(today), 1)) ? "Tomorrow" : null;
          return (
            <section
              key={iso}
              className={`flex min-h-28 flex-col gap-1.5 rounded-lg border bg-surface p-2 ${iso === today ? "border-accent" : "border-border-subtle"}`}
            >
              <Link to="/calendar" search={{ view: "day", date: iso }} className="text-xs font-medium hover:text-accent">
                {relative ?? WEEKDAY_NAMES[(date.getDay() + 6) % 7]}{" "}
                <span className="font-normal text-ink-muted">{date.toLocaleDateString(undefined, { day: "numeric", month: "short" })}</span>
              </Link>
              {entries.map((entry) => (
                <ReleaseCard key={entryKey(entry)} entry={entry} today={today} />
              ))}
              {entries.length === 0 && <span className="text-xs text-ink-muted">Nothing</span>}
            </section>
          );
        })}
      </div>
      {releases.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {releases.error !== null && <p className="text-sm text-danger">The calendar could not be read.</p>}
    </>
  );
}

export function DayView({ date, today }: { date: Date; today: string }) {
  const iso = isoDate(date);
  const releases = useReleases(iso, iso);
  return (
    <section className="flex max-w-2xl flex-col gap-2">
      <h3 className="text-sm font-medium">{date.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: "numeric" })}</h3>
      {releases.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {releases.data && releases.data.length === 0 && <p className="text-sm text-ink-muted">No releases on this day.</p>}
      {(releases.data ?? []).map((entry) => (
        <ReleaseCard key={entryKey(entry)} entry={entry} today={today} />
      ))}
    </section>
  );
}
