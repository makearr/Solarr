import { useState } from "react";
import { Link, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { API_PREFIX, api } from "@/api/client";
import type { components } from "@/api/schema";

type CalendarEntry = components["schemas"]["CalendarEntry"];

const WINDOWS = [
  { label: "This month", back: 0, forward: 31 },
  { label: "±30 days", back: 30, forward: 30 },
  { label: "±90 days", back: 90, forward: 90 },
] as const;

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September",
  "October", "November", "December"] as const;
const WEEKDAYS = ["M", "T", "W", "T", "F", "S", "S"] as const;

/** Releases in the libraries the caller can see: a year of month squares with a dot on every
 *  release day (history included), or the list of a window around today. */
export function Calendar() {
  const search = useSearch({ strict: false }) as { view?: string; year?: string };
  const view = search.view === "list" ? "list" : "year";
  const year = Number(search.year) || new Date().getFullYear();

  return (
    <div className="flex h-full flex-col gap-3 overflow-y-auto p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-medium">Calendar</h2>
        <div className="flex gap-1">
          {(["year", "list"] as const).map((option) => (
            <Link
              key={option}
              to="/calendar"
              search={{ view: option, year: String(year) }}
              aria-current={view === option ? "page" : undefined}
              className={`rounded-md border px-2 py-1 text-xs ${
                view === option
                  ? "border-accent bg-accent text-accent-ink"
                  : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {option === "year" ? "Year" : "List"}
            </Link>
          ))}
        </div>
      </div>
      <p className="text-xs text-ink-muted">
        Releases by artists you follow, and albums you have asked for, in the libraries you can see.
        Dates come from the metadata provider and are sometimes only a month or a year.
      </p>
      <FeedPanel />
      {view === "year" ? <YearView year={year} /> : <ListView />}
    </div>
  );
}

/** A link for a calendar client (2026-09-19): the per-user token in it is the credential, so
 *  it carries only this user's libraries, and a new link revokes the old one. */
function FeedPanel() {
  const queryClient = useQueryClient();
  const [copied, setCopied] = useState(false);
  const feed = useQuery({
    queryKey: ["calendarFeed"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me/calendar-feed");
      if (error) throw error;
      return data;
    },
  });
  const update = (data: { token?: string }) => queryClient.setQueryData(["calendarFeed"], data);
  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/me/calendar-feed");
      if (error) throw error;
      return data;
    },
    onSuccess: update,
  });
  const revoke = useMutation({
    mutationFn: async () => {
      const { error } = await api.DELETE("/me/calendar-feed");
      if (error) throw error;
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
    queryFn: async () => {
      const { data, error } = await api.GET("/calendar/dates", {
        params: { query: { from: String(year), to: `${year}-12-31` } },
      });
      if (error) throw error;
      return data;
    },
  });

  const counts = new Map<string, number>();
  for (const row of dates.data ?? []) counts.set(row.date, row.releases);
  const yearOnly = counts.get(String(year)) ?? 0;
  const today = new Date().toISOString().slice(0, 10);

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
              onClick={() => onPick({ label: new Date(date).toLocaleDateString(), from: date, to: date })}
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
    queryFn: async () => {
      const { data, error } = await api.GET("/calendar", {
        params: { query: { from: pick.from, to: pick.to, limit: 500 } },
      });
      if (error) throw error;
      return data;
    },
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
    queryFn: async () => {
      const { data, error } = await api.GET("/calendar", {
        params: { query: { from, to, limit: 300 } },
      });
      if (error) throw error;
      return data;
    },
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
