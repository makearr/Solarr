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

// Browsers allow six connections per origin over HTTP/1.1, and every image and API call shares them: one stream per
// browser, not per tab. The tab holding the "leader" lock reads the stream and relays its frames to the other tabs of
// the same origin over a BroadcastChannel; they tell it which topics they need. Without Web Locks every tab reads its own.
const LEADER_LOCK = "solarr-event-stream-leader";
const CHANNEL_NAME = "solarr-event-stream";
const HEARTBEAT_MS = 10_000;
const FOLLOWER_EXPIRES_MS = 30_000;
const tabId = Math.random().toString(36).slice(2);

type Relay =
  | { kind: "frame"; events: StreamEvent[] }
  | { kind: "resync" }
  | { kind: "connection"; value: Connection }
  | { kind: "topics"; tab: string; topics: string | null }
  | { kind: "hello" };

let channel: BroadcastChannel | undefined;
let sharing = false;
let leader = false;
const followers = new Map<string, { topics: string; seen: number }>();

function startSharing() {
  if (sharing || typeof BroadcastChannel === "undefined" || typeof navigator === "undefined" || !navigator.locks) return;
  sharing = true;
  channel = new BroadcastChannel(CHANNEL_NAME);
  channel.onmessage = (message: MessageEvent<Relay>) => onRelay(message.data);
  setInterval(tick, HEARTBEAT_MS);
  void navigator.locks.request(LEADER_LOCK, () => {
    leader = true;
    channel?.postMessage({ kind: "hello" } satisfies Relay);
    schedule();
    // Held until this tab closes, when the browser hands the lock to the next tab waiting for it.
    return new Promise<void>(() => {});
  });
  window.addEventListener("pagehide", () => channel?.postMessage({ kind: "topics", tab: tabId, topics: null } satisfies Relay));
}

function onRelay(relay: Relay) {
  switch (relay.kind) {
    case "frame":
      if (!leader) dispatch(relay.events);
      break;
    case "resync":
      if (!leader) for (const subscriber of [...subscribers]) subscriber.onResync?.();
      break;
    case "connection":
      if (!leader) setConnection(relay.value);
      break;
    case "hello":
      announceTopics();
      break;
    case "topics":
      if (!leader) break;
      if (relay.topics === null) followers.delete(relay.tab);
      else followers.set(relay.tab, { topics: relay.topics, seen: Date.now() });
      schedule();
      break;
  }
}

/** A follower tells the leader what it needs; the leader prunes the tabs that went silent. */
function announceTopics() {
  if (leader || !channel) return;
  const wanted = wantedTopics();
  channel.postMessage({ kind: "topics", tab: tabId, topics: wanted ?? null } satisfies Relay);
}

function tick() {
  if (leader) {
    const now = Date.now();
    let pruned = false;
    for (const [tab, one] of followers) {
      if (now - one.seen > FOLLOWER_EXPIRES_MS) {
        followers.delete(tab);
        pruned = true;
      }
    }
    if (pruned) schedule();
    return;
  }
  announceTopics();
}

function dispatch(events: readonly StreamEvent[]) {
  for (const subscriber of [...subscribers]) {
    const mine = subscriber.topics.length === 0 ? events : events.filter((event) => subscriber.topics.includes(event.topic));
    if (mine.length > 0) subscriber.onEvents?.(mine);
  }
}

export function subscribe<E extends StreamEvent>(subscriber: Subscriber<E>): () => void {
  const held = subscriber as unknown as Subscriber;
  startSharing();
  subscribers.add(held);
  held.onConnection?.(connection);
  schedule();
  return () => {
    subscribers.delete(held);
    schedule();
  };
}

/** The topics query the subscribers need: undefined for nobody, "" for everything. A leader answers for the other tabs too. */
function wantedTopics(): string | undefined {
  const union = new Set<string>();
  let anyone = subscribers.size > 0;
  const take = (topics: readonly string[]) => {
    if (topics.length === 0) return true;
    for (const topic of topics) union.add(topic);
    return false;
  };
  for (const subscriber of subscribers) if (take(subscriber.topics)) return "";
  if (leader) {
    for (const one of followers.values()) {
      anyone = true;
      if (one.topics === "") return "";
      take(one.topics.split(","));
    }
  }
  return anyone ? [...union].sort().join(",") : undefined;
}

function schedule() {
  if (!leader) announceTopics();
  clearTimeout(timer);
  // The first stream waits only for the rest of the render's effects, so it opens with all their topics.
  timer = setTimeout(reconcile, source === undefined ? 0 : RECONCILE_DELAY_MS);
}

function reconcile() {
  timer = undefined;
  if (sharing && !leader) return;
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
    dispatch(events);
    if (leader) channel?.postMessage({ kind: "frame", events: [...events] } satisfies Relay);
  });
  opened.addEventListener("resync", () => {
    for (const subscriber of [...subscribers]) subscriber.onResync?.();
    if (leader) channel?.postMessage({ kind: "resync" } satisfies Relay);
  });
}

function setConnection(next: Connection) {
  connection = next;
  for (const subscriber of [...subscribers]) subscriber.onConnection?.(next);
  if (leader) channel?.postMessage({ kind: "connection", value: next } satisfies Relay);
}
