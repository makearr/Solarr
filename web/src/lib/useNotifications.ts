import { useCallback, useEffect, useRef, useState } from "react";

import { API_PREFIX } from "@/api/client";
import type { components } from "@/api/schema";

export type Connection = "connecting" | "live" | "dropped";

export interface Notification {
  readonly id: number;
  readonly topic: string;
  readonly entity: string;
  readonly kind: string;
  readonly at: string;
  /** Present on download history events: what was grabbed, imported or failed. */
  readonly data?: {
    readonly release_title?: string;
    readonly album?: string;
    readonly artist?: string;
    readonly detail?: string;
  };
}

const DOWNLOAD_VERBS: Record<string, string> = {
  "download.grabbed": "Grabbed",
  "download.imported": "Imported",
  "download.failed": "Download failed",
};

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

/** Plain language for the bar: a download reads as a sentence, anything else as "job:42 updated". */
export function describe(event: Notification): string {
  const verb = DOWNLOAD_VERBS[event.kind];
  if (verb !== undefined && event.data) {
    const album = [event.data.artist, event.data.album].filter(Boolean).join(" — ");
    const what = album || event.data.release_title || event.entity;
    return event.data.detail ? `${verb}: ${what} (${event.data.detail})` : `${verb}: ${what}`;
  }
  return `${event.entity} ${event.kind}`;
}

/** A stored history entry, shaped like the live event that announced it. */
export function historyNotification(entry: components["schemas"]["HistoryEntry"]): Notification {
  return {
    id: entry.id, topic: "acquisition", entity: `work:${entry.work_id ?? 0}`, kind: `download.${entry.kind}`, at: entry.at,
    data: { release_title: entry.release_title, album: entry.album, artist: entry.artist, detail: entry.detail },
  };
}
