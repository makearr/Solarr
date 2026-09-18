import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import { useDebouncedValue } from "./useDebouncedValue";

const LEVELS = ["", "debug", "info", "warn", "error"] as const;
type Level = (typeof LEVELS)[number];

/** What this process has logged since it started (revision 7.16). A bounded
 *  in-memory buffer, not a log file: nothing survives a restart, and raising
 *  the configured log level quiets this too. */
export function Logs() {
  const [level, setLevel] = useState<Level>("");
  const [query, setQuery] = useState("");
  const debounced = useDebouncedValue(query.trim(), 250);

  const records = useQuery({
    queryKey: ["logs", level, debounced],
    queryFn: async () => {
      const { data, error } = await api.GET("/system/logs", {
        params: {
          query: {
            ...(level === "" ? {} : { level }),
            ...(debounced === "" ? {} : { q: debounced }),
            limit: 500,
          },
        },
      });
      if (error) throw error;
      return data;
    },
    refetchInterval: 5000,
  });

  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-medium">Logs</h2>
        <div className="flex gap-1">
          {LEVELS.map((option) => (
            <button
              key={option || "all"}
              type="button"
              aria-pressed={level === option}
              onClick={() => setLevel(option)}
              className={`rounded-md border px-2 py-1 text-xs capitalize ${
                level === option
                  ? "border-accent bg-accent text-accent-ink"
                  : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {option || "all"}
            </button>
          ))}
        </div>
        <input
          type="search"
          value={query}
          placeholder="Filter…"
          onChange={(event) => setQuery(event.target.value)}
          className="w-48 rounded-md border border-border-subtle bg-surface px-2 py-1 text-sm"
        />
      </div>
      <p className="text-xs text-ink-muted">
        Kept in memory since this process started — not a log file. Nothing here survives a
        restart.
      </p>

      {records.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {records.error !== null && <p className="text-sm text-ink-muted">The log could not be read.</p>}
      {records.data && records.data.length === 0 && (
        <p className="text-sm text-ink-muted">Nothing matches.</p>
      )}

      {records.data && records.data.length > 0 && (
        <ul className="flex min-h-0 flex-1 flex-col overflow-y-auto rounded-md border border-border-subtle bg-surface">
          {records.data.map((record, index) => (
            <li
              key={`${record.time}:${index}`}
              className="flex gap-3 border-b border-border-subtle px-3 py-1.5 font-mono text-xs last:border-b-0"
            >
              <span className="shrink-0 text-ink-muted">
                {new Date(record.time).toLocaleTimeString()}
              </span>
              <span className={`w-12 shrink-0 ${levelClass(record.level)}`}>{record.level}</span>
              <span className="min-w-0 flex-1">
                {record.message}
                {record.attrs != null && record.attrs.length > 0 && (
                  <span className="pl-2 text-ink-muted">{record.attrs.join(" ")}</span>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function levelClass(level: string): string {
  if (level === "ERROR") return "text-danger";
  if (level === "WARN") return "text-ink";
  return "text-ink-muted";
}
