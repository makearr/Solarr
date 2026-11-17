import { API_PREFIX } from "@/api/client";

export type Connection = "connecting" | "live" | "dropped";

/** One event of a frame; the server names its topic, so each subscriber filters for itself. */
export interface StreamEvent {
  readonly topic: string;
  readonly entity: string;
}

export interface Subscriber<E extends StreamEvent = StreamEvent> {
  /** Empty means every topic. */
  readonly topics: readonly string[];
  /** Called with the frame's events of the subscriber's topics, never with none. */
  readonly onEvents?: (events: readonly E[]) => void;
  /** The server dropped events this tab had not seen yet. */
  readonly onResync?: () => void;
  readonly onConnection?: (connection: Connection) => void;
}

/** Long enough that a screen change, which unsubscribes and resubscribes in one tick, keeps its connection. */
const RECONCILE_DELAY_MS = 250;

const subscribers = new Set<Subscriber>();
let source: EventSource | undefined;
let openTopics: string | undefined;
let connection: Connection = "connecting";
let timer: ReturnType<typeof setTimeout> | undefined;

// Browsers allow six connections per origin over HTTP/1.1, and every image and API call shares them: one stream per tab.
export function subscribe<E extends StreamEvent>(subscriber: Subscriber<E>): () => void {
  const held = subscriber as unknown as Subscriber;
  subscribers.add(held);
  held.onConnection?.(connection);
  schedule();
  return () => {
    subscribers.delete(held);
    schedule();
  };
}

/** The topics query the subscribers need: undefined for nobody, "" for everything. */
function wantedTopics(): string | undefined {
  if (subscribers.size === 0) return undefined;
  const union = new Set<string>();
  for (const subscriber of subscribers) {
    if (subscriber.topics.length === 0) return "";
    for (const topic of subscriber.topics) union.add(topic);
  }
  return [...union].sort().join(",");
}

function schedule() {
  clearTimeout(timer);
  // The first stream waits only for the rest of the render's effects, so it opens with all their topics.
  timer = setTimeout(reconcile, source === undefined ? 0 : RECONCILE_DELAY_MS);
}

function reconcile() {
  timer = undefined;
  const wanted = wantedTopics();
  if (wanted === openTopics) return;
  source?.close();
  source = undefined;
  openTopics = wanted;
  if (wanted === undefined) {
    setConnection("connecting");
    return;
  }
  open(wanted);
}

function open(topics: string) {
  const opened = new EventSource(topics === "" ? `${API_PREFIX}/events` : `${API_PREFIX}/events?topics=${topics}`, {
    withCredentials: true,
  });
  source = opened;
  setConnection("connecting");
  opened.onopen = () => setConnection("live");
  opened.onerror = () => setConnection("dropped");
  // The server names every frame "update" and coalesces several events into one (architecture section 13.4).
  opened.addEventListener("update", (message) => {
    let events: readonly StreamEvent[];
    try {
      events = (JSON.parse((message as MessageEvent<string>).data) as { events: StreamEvent[] }).events;
    } catch {
      // A frame we cannot read is not worth tearing the stream down for.
      return;
    }
    for (const subscriber of [...subscribers]) {
      const mine = subscriber.topics.length === 0 ? events : events.filter((event) => subscriber.topics.includes(event.topic));
      if (mine.length > 0) subscriber.onEvents?.(mine);
    }
  });
  opened.addEventListener("resync", () => {
    for (const subscriber of [...subscribers]) subscriber.onResync?.();
  });
}

function setConnection(next: Connection) {
  connection = next;
  for (const subscriber of [...subscribers]) subscriber.onConnection?.(next);
}
