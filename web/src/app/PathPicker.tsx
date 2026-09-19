import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronUp, Clock, Folder, FolderOpen } from "lucide-react";

import { api } from "@/api/client";
import { usePreference } from "@/lib/usePreference";

import { useDebouncedValue } from "./useDebouncedValue";

// How many previously-used paths are remembered per user.
const RECENT_LIMIT = 8;

/** The paths this user has imported from before, newest first, and the way
 *  to add one. Stored per user like every other interface preference, not
 *  derived from the session list -- a dismissed session must not take its
 *  own folder out of the history. */
export function useRecentPaths(): [readonly string[], (path: string) => void] {
  const [recent, setRecent] = usePreference<string[]>("import.recentPaths", []);
  const remember = (path: string) => {
    const trimmed = path.trim();
    if (trimmed === "") return;
    setRecent([trimmed, ...recent.filter((entry) => entry !== trimmed)].slice(0, RECENT_LIMIT));
  };
  return [recent, remember];
}

/** A path typed, browsed or picked from history. The field itself stays a
 *  plain text input -- a path can always be pasted -- and the panel beneath
 *  it lists whatever directory the text currently points at, so browsing and
 *  typeahead are the same gesture (2026-09-19 review). */
export function PathPicker({
  value,
  onChange,
  recent,
  placeholder,
  label = "Source path",
}: {
  value: string;
  onChange: (path: string) => void;
  recent: readonly string[];
  placeholder?: string;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const fieldID = useId();
  const container = useRef<HTMLDivElement>(null);
  const debounced = useDebouncedValue(value, 250);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (!container.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const listing = useQuery({
    queryKey: ["browse", debounced],
    enabled: open,
    queryFn: async () => {
      const { data, error } = await api.GET("/storage/browse", {
        params: { query: { path: debounced === "" ? undefined : debounced, dirs_only: true } },
      });
      if (error) throw error;
      return data;
    },
    retry: false,
  });

  // A trailing slash is what tells the endpoint to list inside a directory
  // rather than treat its name as a prefix, so picking one goes down a level.
  const descend = (path: string) => onChange(`${path}/`);

  return (
    <div ref={container} className="relative flex min-w-64 flex-1 flex-col gap-1 text-xs text-ink-muted">
      <label htmlFor={fieldID}>{label}</label>
      <div className="flex items-center gap-1">
        <input
          id={fieldID}
          className={`${inputStyle} min-w-0 flex-1`}
          value={value}
          placeholder={placeholder}
          autoComplete="off"
          onFocus={() => setOpen(true)}
          onChange={(event) => {
            onChange(event.target.value);
            setOpen(true);
          }}
        />
        <button
          type="button"
          aria-label="Browse folders"
          aria-expanded={open}
          className="shrink-0 rounded-md border border-border-subtle p-1.5 text-ink-muted hover:text-ink
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          onClick={() => setOpen((shown) => !shown)}
        >
          <FolderOpen aria-hidden className="size-4" />
        </button>
      </div>

      {open && (
        <div
          className="absolute top-full z-20 mt-1 max-h-72 w-full overflow-y-auto rounded-md border
                     border-border-subtle bg-surface shadow-lg"
        >
          {recent.length > 0 && (
            <div className="border-b border-border-subtle">
              <p className="px-2 py-1 text-[0.6875rem] uppercase tracking-wide text-ink-muted">Recent</p>
              {recent.map((path) => (
                <Row key={path} icon={<Clock aria-hidden className="size-3.5" />} label={path}
                     onPick={() => { onChange(path); setOpen(false); }} />
              ))}
            </div>
          )}
          {listing.isError && <p className="p-2 text-danger">No such folder.</p>}
          {listing.data && (
            <>
              <p className="px-2 py-1 font-mono text-[0.6875rem] text-ink-muted">{listing.data.path}</p>
              {listing.data.parent !== undefined && (
                <Row
                  icon={<ChevronUp aria-hidden className="size-3.5" />}
                  label="Up one level"
                  onPick={() => descend(listing.data.parent!)}
                />
              )}
              {listing.data.entries.length === 0 && (
                <p className="p-2 text-ink-muted">No folders here.</p>
              )}
              {listing.data.entries.map((entry) => (
                <Row
                  key={entry.path}
                  icon={<Folder aria-hidden className="size-3.5" />}
                  label={entry.name}
                  onPick={() => descend(entry.path)}
                />
              ))}
              {listing.data.truncated && (
                <p className="p-2 text-ink-muted">More folders here than can be listed -- keep typing.</p>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function Row({
  icon,
  label,
  onPick,
}: {
  icon: React.ReactNode;
  label: string;
  onPick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onPick}
      className="flex w-full items-center gap-2 px-2 py-1 text-left text-ink hover:bg-canvas
                 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-accent"
    >
      <span className="shrink-0 text-ink-muted">{icon}</span>
      <span className="truncate font-mono">{label}</span>
    </button>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
