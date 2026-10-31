import { useState } from "react";
import { Link, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { api, API_PREFIX, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useNarrow } from "@/list/useNarrow";
import { addDays, DayView, DaysView, entryKey, entryTarget, isoDate, MonthView, parseIso, weekStart } from "./CalendarViews";

type CalendarEntry = components["schemas"]["CalendarEntry"];

const WINDOWS = [
  { label: "This month", back: 0, forward: 31 },
  { label: "±30 days", back: 30, forward: 30 },
  { label: "±90 days", back: 90, forward: 90 },
] as const;

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September",
  "October", "November", "December"] as const;
const WEEKDAYS = ["M", "T", "W", "T", "F", "S", "S"] as const;

const VIEWS = [
  ["month", "Month"],
  ["week", "Week"],
  ["forecast", "Forecast"],
  ["day", "Day"],
  ["agenda", "Agenda"],
  ["year", "Year"],
] as const;
type View = (typeof VIEWS)[number][0];

/** Releases in the libraries the caller can see, Lidarr-style: month, week, a seven-day forecast,
 *  one day, an agenda list, and a year of month squares with a dot on every release day. */
export function Calendar() {
  const search = useSearch({ strict: false }) as { view?: string; year?: string; date?: string };
  const requested = search.view === "list" ? "agenda" : search.view;
  // A phone opens on the agenda, as Sonarr's does: a month grid that narrow shows one letter per release.
  const fallback: View = useNarrow() ? "agenda" : "month";
  const view: View = VIEWS.some(([id]) => id === requested) ? (requested as View) : fallback;
  const year = Number(search.year) || new Date().getFullYear();
  const today = isoDate(new Date());
  const anchor = parseIso(search.date);
  const moved = (direction: number) => {
    const next = new Date(anchor);
    if (view === "month") next.setMonth(next.getMonth() + direction, 1);
    else next.setDate(next.getDate() + direction * (view === "week" ? 7 : 1));
    return isoDate(next);
  };
  const heading =
    view === "month"
      ? `${MONTHS[anchor.getMonth()]} ${anchor.getFullYear()}`
      : view === "week"
        ? `${weekStart(anchor).toLocaleDateString(undefined, { day: "numeric", month: "short" })} - ${addDays(weekStart(anchor), 6).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}`
        : view === "day"
          ? anchor.toLocaleDateString()
          : view === "forecast"
            ? "Next 7 days"
            : view === "year"
              ? String(year)
              : "Coming and recent";
  const stepped = view === "month" || view === "week" || view === "day";

  return (
    <div className="flex h-full flex-col gap-3 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-sm font-medium">Calendar</h1>
        {stepped && (
          <div className="flex items-center gap-1">
            <Link
              to="/calendar"
              search={{ view, date: moved(-1) }}
              aria-label="Previous"
              className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-ink"
            >
              <ChevronLeft aria-hidden className="size-4" />
            </Link>
            <Link
              to="/calendar"
              search={{ view, date: today }}
              className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink"
            >
              Today
            </Link>
            <Link
              to="/calendar"
              search={{ view, date: moved(1) }}
              aria-label="Next"
              className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-ink"
            >
              <ChevronRight aria-hidden className="size-4" />
            </Link>
          </div>
        )}
        <span className="text-sm tabular-nums">{heading}</span>
        <div className="ml-auto flex flex-wrap gap-1">
          {VIEWS.map(([id, label]) => (
            <Link
              key={id}
              to="/calendar"
              search={{ view: id, year: String(year), date: search.date }}
              aria-current={view === id ? "page" : undefined}
              className={`rounded-md border px-2 py-1 text-xs ${
                view === id
                  ? "border-accent bg-accent text-accent-ink"
                  : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {label}
            </Link>
          ))}
        </div>
      </div>
      <p className="flex flex-wrap items-center gap-x-3 text-xs text-ink-muted">
        <span>
          What you follow or have asked for (albums, episodes, films, books) in the libraries you can see. Dates come from
          the metadata provider and are sometimes only a month or a year.
        </span>
        <span className="flex items-center gap-2">
          <Key className="border-l-ok" label="have it" />
          <Key className="border-l-accent" label="wanted, not out yet" />
          <Key className="border-l-danger" label="wanted, out" />
        </span>
      </p>
      <FeedPanel />
      {view === "month" && <MonthView anchor={anchor} today={today} />}
      {view === "week" && <DaysView start={weekStart(anchor)} days={7} today={today} />}
      {view === "forecast" && <DaysView start={parseIso(today)} days={7} today={today} />}
      {view === "day" && <DayView date={anchor} today={today} />}
      {view === "agenda" && <ListView />}
      {view === "year" && <YearView year={year} />}
    </div>
  );
}

function Key({ className, label }: { className: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span aria-hidden className={`inline-block h-3 w-1 rounded-sm border-l-4 ${className}`} />
      {label}
    </span>
  );
}

/** A link for a calendar client (2026-09-19): the per-user token in it is the credential, so
 *  it carries only this user's libraries, and a new link revokes the old one. */
function FeedPanel() {
  const queryClient = useQueryClient();
  const [copied, setCopied] = useState(false);
  const feed = useQuery({
    queryKey: ["calendarFeed"],
    queryFn: () => ok(api.GET("/me/calendar-feed")),
  });
  const update = (data: { token?: string }) => queryClient.setQueryData(["calendarFeed"], data);
  const create = useMutation({
    mutationFn: () => ok(api.POST("/me/calendar-feed")),
    onSuccess: update,
  });
  const revoke = useMutation({
    mutationFn: async () => {
      await ok(api.DELETE("/me/calendar-feed"));
    },
    onSuccess: () => update({}),
  });

  const token = feed.data?.token;
  const link = token ? `${window.location.origin}${API_PREFIX}/calendar/feed.ics?token=${token}` : undefined;

  return (
    <section className="flex flex-wrap items-center gap-2 rounded-lg border border-border-subtle bg-surface px-3 py-2 text-xs">
      <span className="font-medium">Calendar link</span>
      {link ? (
        <>
          <input
            readOnly
            value={link}
            aria-label="Calendar link"
            onFocus={(event) => event.target.select()}
            className="min-w-0 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 font-mono text-[0.6875rem]"
          />
          <button
            type="button"
            onClick={() => {
              void navigator.clipboard?.writeText(link).then(() => setCopied(true));
            }}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink"
          >
            {copied ? "Copied" : "Copy"}
          </button>
          <button
            type="button"
            disabled={create.isPending}
            onClick={() => {
              if (window.confirm("Make a new link? The current one stops working.")) {
                setCopied(false);
                create.mutate();
              }
            }}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink"
          >
            New link
          </button>
          <button
            type="button"
            disabled={revoke.isPending}
            onClick={() => revoke.mutate()}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-danger"
          >
            Revoke
          </button>
        </>
      ) : (
        <>
          <span className="text-ink-muted">
            Subscribe from a calendar app. The link shows only your libraries; anyone who has it can
            read your calendar.
          </span>
          <button
            type="button"
            disabled={create.isPending || feed.isLoading}
            onClick={() => create.mutate()}
            className="ml-auto rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50"
          >
            Get a link
          </button>
        </>
      )}
      {(create.isError || revoke.isError) && (
        <span className="text-danger">
          {((create.error ?? revoke.error) as { detail?: string } | null)?.detail ?? "That did not work."}
        </span>
      )}
    </section>
  );
}

type Pick = { label: string; from: string; to: string };

function YearView({ year }: { year: number }) {
  const [picked, setPicked] = useState<Pick | undefined>();
  const dates = useQuery({
    queryKey: ["calendar", "dates", year],
    queryFn: () => ok(api.GET("/calendar/dates", {
      params: { query: { from: String(year), to: `${year}-12-31` } },
    })),
  });

  const counts = new Map<string, number>();
  for (const row of dates.data ?? []) counts.set(row.date, row.releases);
  const yearOnly = counts.get(String(year)) ?? 0;
  const today = isoDate(new Date());

  return (
    <>
      <div className="flex items-center gap-2">
        <Link
          to="/calendar"
          search={{ view: "year", year: String(year - 1) }}
          aria-label="Previous year"
          className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-ink"
        >
          <ChevronLeft aria-hidden className="size-4" />
        </Link>
        <span className="w-14 text-center text-sm font-medium tabular-nums">{year}</span>
        <Link
          to="/calendar"
          search={{ view: "year", year: String(year + 1) }}
          aria-label="Next year"
          className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-ink"
        >
          <ChevronRight aria-hidden className="size-4" />
        </Link>
        {yearOnly > 0 && (
          <button
            type="button"
            onClick={() => setPicked({ label: `Dated only ${year}`, from: String(year), to: String(year) })}
            className="text-xs text-ink-muted underline-offset-2 hover:text-ink hover:underline"
          >
            {yearOnly} {yearOnly === 1 ? "release" : "releases"} dated only {year}
          </button>
        )}
        {dates.isLoading && <span className="text-xs text-ink-muted">Loading…</span>}
        {dates.error !== null && <span className="text-xs text-danger">The calendar could not be read.</span>}
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {MONTHS.map((name, index) => (
          <MonthSquare
            key={name}
            year={year}
            month={index}
            name={name}
            counts={counts}
            today={today}
            picked={picked}
            onPick={setPicked}
          />
        ))}
      </div>

      {picked && <PickedReleases pick={picked} onClose={() => setPicked(undefined)} />}
    </>
  );
}

function MonthSquare({
  year,
  month,
  name,
  counts,
  today,
  picked,
  onPick,
}: {
  year: number;
  month: number;
  name: string;
  counts: Map<string, number>;
  today: string;
  picked: Pick | undefined;
  onPick: (pick: Pick) => void;
}) {
  const prefix = `${year}-${String(month + 1).padStart(2, "0")}`;
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  // Monday-first, as a European calendar reads.
  const leading = (new Date(year, month, 1).getDay() + 6) % 7;
  const monthOnly = counts.get(prefix) ?? 0;
  let total = monthOnly;
  for (let day = 1; day <= daysInMonth; day++) total += counts.get(`${prefix}-${String(day).padStart(2, "0")}`) ?? 0;

  return (
    <section className="flex flex-col gap-1.5 rounded-lg border border-border-subtle bg-surface p-3">
      <button
        type="button"
        onClick={() => onPick({ label: `${name} ${year}`, from: prefix, to: `${prefix}-31` })}
        className="flex items-baseline gap-2 text-left text-sm font-medium hover:text-accent"
      >
        {name}
        {total > 0 && <span className="text-xs font-normal text-ink-muted">{total}</span>}
      </button>
      <div className="grid grid-cols-7 gap-0.5 text-center text-[0.625rem] text-ink-muted">
        {WEEKDAYS.map((weekday, index) => (
          <span key={index}>{weekday}</span>
        ))}
        {Array.from({ length: leading }, (_, index) => (
          <span key={`blank-${index}`} />
        ))}
        {Array.from({ length: daysInMonth }, (_, index) => {
          const date = `${prefix}-${String(index + 1).padStart(2, "0")}`;
          const releases = counts.get(date) ?? 0;
          const isPicked = picked?.from === date && picked.to === date;
          return (
            <button
              key={date}
              type="button"
              disabled={releases === 0}
              title={releases > 0 ? `${releases} ${releases === 1 ? "release" : "releases"}` : undefined}
              onClick={() => onPick({ label: parseIso(date).toLocaleDateString(), from: date, to: date })}
              className={`flex h-6 flex-col items-center justify-center rounded tabular-nums ${
                date === today ? "ring-1 ring-accent" : ""
              } ${isPicked ? "bg-accent/15" : ""} ${releases > 0 ? "text-ink hover:bg-canvas" : ""}`}
            >
              {index + 1}
              <span
                aria-hidden
                className={`size-1.5 rounded-full ${
                  releases === 0 ? "bg-transparent" : date < today ? "bg-ink-muted" : "bg-accent"
                }`}
              />
            </button>
          );
        })}
      </div>
      {monthOnly > 0 && (
        <button
          type="button"
          onClick={() => onPick({ label: `Dated only ${name} ${year}`, from: prefix, to: prefix })}
          className="self-start text-[0.6875rem] text-ink-muted hover:text-ink"
        >
          + {monthOnly} dated only {name}
        </button>
      )}
    </section>
  );
}

function PickedReleases({ pick, onClose }: { pick: Pick; onClose: () => void }) {
  const entries = useQuery({
    queryKey: ["calendar", pick.from, pick.to],
    queryFn: () => ok(api.GET("/calendar", {
      params: { query: { from: pick.from, to: pick.to, limit: 500 } },
    })),
  });
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h3 className="text-sm font-medium">{pick.label}</h3>
        <button type="button" onClick={onClose} className="text-xs text-ink-muted hover:text-ink">
          Close
        </button>
      </div>
      {entries.isLoading && <Message>Loading…</Message>}
      {entries.data && entries.data.length === 0 && <Message>Nothing here.</Message>}
      {entries.data && <ReleaseList entries={entries.data} />}
    </section>
  );
}

function ListView() {
  const [windowIndex, setWindowIndex] = useState(1);
  const chosen = WINDOWS[windowIndex] ?? WINDOWS[1]!;
  const { from, to } = windowDates(chosen.back, chosen.forward);

  const entries = useQuery({
    queryKey: ["calendar", from, to],
    queryFn: () => ok(api.GET("/calendar", {
      params: { query: { from, to, limit: 300 } },
    })),
  });

  return (
    <>
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
      {entries.isLoading && <Message>Loading…</Message>}
      {entries.error !== null && <Message>The calendar could not be read.</Message>}
      {entries.data && entries.data.length === 0 && (
        <Message>
          Nothing in this window. Monitor an artist, or want an album, and its releases appear here.
        </Message>
      )}
      {entries.data && <ReleaseList entries={entries.data} />}
    </>
  );
}

function ReleaseList({ entries }: { entries: readonly CalendarEntry[] }) {
  const byDate = new Map<string, CalendarEntry[]>();
  for (const entry of entries) {
    const existing = byDate.get(entry.release_date);
    if (existing) existing.push(entry);
    else byDate.set(entry.release_date, [entry]);
  }
  return (
    <ul className="flex flex-col gap-3">
      {[...byDate.entries()].map(([date, releases]) => (
        <li key={date} className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink-muted">{formatDate(date)}</span>
          <ul className="flex flex-col gap-1">
            {releases.map((release) => (
              <li
                key={entryKey(release)}
                className="flex flex-wrap items-center gap-x-3 gap-y-0.5 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm sm:flex-nowrap"
              >
                <Link
                  {...entryTarget(release)}
                  className="min-w-0 basis-full truncate hover:underline sm:flex-1 sm:basis-0"
                >
                  {release.title}
                </Link>
                {release.artist != null && release.artist !== "" && (
                  <span className="min-w-0 flex-1 truncate text-xs text-ink-muted sm:flex-none sm:shrink-0">{release.artist}</span>
                )}
                {release.primary_type != null && (
                  <span className="shrink-0 text-xs text-ink-muted">{release.primary_type}</span>
                )}
                <span className={`shrink-0 text-xs ${release.on_disk ? "text-ink-muted" : "text-accent"}`}>
                  {release.on_disk ? "have it" : "wanted"}
                </span>
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}

/** A partial date is shown as given: "2024-03" means the month is all the
 *  provider knows, and inventing a day would be a claim nobody made. */
function formatDate(date: string): string {
  if (date.length === 10) return parseIso(date).toLocaleDateString();
  return date;
}

function windowDates(back: number, forward: number): { from: string; to: string } {
  const now = new Date();
  const start = new Date(now);
  start.setDate(start.getDate() - back);
  const end = new Date(now);
  end.setDate(end.getDate() + forward);
  return { from: isoDate(start), to: isoDate(end) };
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
