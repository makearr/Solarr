import { useCallback, useEffect, useRef, useState } from "react";

import { API_PREFIX } from "@/api/client";

export type Connection = "connecting" | "live" | "dropped";

export interface Notification {
  readonly id: number;
  readonly topic: string;
  readonly entity: string;
  readonly kind: string;
  readonly at: string;
}

/** How much scrollback the bar keeps. Enough to answer "what just happened",
 *  far short of being a history view -- that is what the event log is for. */
const SCROLLBACK = 50;

interface Stream {
  readonly recent: readonly Notification[];
  readonly unread: number;
  readonly connection: Connection;
  readonly markRead: () => void;
}

/** The status bar's feed. EventSource reconnects on its own, so this reports
 *  the connection rather than managing it -- a silently dead stream is the
 *  failure worth surfacing, because the UI keeps looking fine while going
 *  stale. */
export function useNotifications(): Stream {
  const [recent, setRecent] = useState<readonly Notification[]>([]);
  const [unread, setUnread] = useState(0);
  const [connection, setConnection] = useState<Connection>("connecting");
  // Held in a ref so arriving events do not re-subscribe the stream.
  const seen = useRef(0);

  useEffect(() => {
    const source = new EventSource(`${API_PREFIX}/events`, { withCredentials: true });

    source.onopen = () => setConnection("live");
    source.onerror = () => setConnection("dropped");
    // The server always names a live or replayed frame "update" (never the
    // default unnamed message type) and coalesces several events into one
    // frame every 250ms (architecture section 13.4), so one frame can carry
    // more than one notification.
    source.addEventListener("update", (message) => {
      let batch: { events: Notification[] };
      try {
        batch = JSON.parse(message.data) as { events: Notification[] };
      } catch {
        // A frame we cannot read is not worth tearing the stream down for.
        return;
      }
      if (batch.events.length === 0) return;
      // The bus does not order one flush's own batch, so oldest-to-newest is
      // sorted here before reversing it into "most recent first".
      const ordered = [...batch.events].sort((a, b) => a.id - b.id).reverse();
      setRecent((held) => ordered.concat(held).slice(0, SCROLLBACK));
      setUnread((count) => count + batch.events.length);
      seen.current += batch.events.length;
    });

    return () => source.close();
  }, []);

  const markRead = useCallback(() => setUnread(0), []);
  return { recent, unread, connection, markRead };
}

/** Plain language for the bar. The entity is already "job:42" shaped, so this
 *  reads as "job:42 updated" rather than inventing a sentence per topic. */
export function describe(event: Notification): string {
  return `${event.entity} ${event.kind}`;
}
