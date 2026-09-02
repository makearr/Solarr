import { useState } from "react";

import { SKEW_TOLERANCE_MS, useServerClock } from "@/lib/useServerClock";
import { describe, useNotifications, type Connection } from "@/lib/useNotifications";

/** The bottom bar: what just happened on the left, the clock on the right.
 *  Persistent rather than transient -- covering content with a toast is one of
 *  the complaints this project exists to answer. */
export function StatusBar() {
  const { recent, unread, connection, markRead } = useNotifications();
  const clock = useServerClock();
  const [open, setOpen] = useState(false);

  const latest = recent[0];

  return (
    <footer className="relative shrink-0 border-t border-border-subtle bg-surface">
      {open && recent.length > 0 && (
        <ul className="absolute bottom-full left-0 max-h-64 w-96 overflow-y-auto border border-b-0
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

      <div className="flex items-center gap-3 px-4 py-1.5 text-xs">
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
            {latest ? describe(latest) : "Nothing has happened yet"}
          </span>
          {unread > 0 && (
            <span className="shrink-0 rounded-full bg-accent px-1.5 py-px text-[0.6875rem]
                             font-medium text-accent-ink">
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </button>

        <div className="ml-auto flex shrink-0 items-center gap-3 text-ink-muted">
          {clock.skewMs !== null && Math.abs(clock.skewMs) > SKEW_TOLERANCE_MS && (
            <span className="text-warn" title="This browser's clock disagrees with the server's">
              clock skew {Math.round(clock.skewMs / 1000)}s
            </span>
          )}
          {clock.time && (
            <span className="font-mono tabular-nums">
              {clock.time}
              <span className="ml-1.5 text-ink-muted/70">{clock.timezone}</span>
            </span>
          )}
        </div>
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
