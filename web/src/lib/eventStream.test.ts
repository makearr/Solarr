import { afterEach, beforeEach, expect, test, vi } from "vitest";

/** Every stream opened, so a test counts connections rather than trusting an effect. */
const opened: FakeEventSource[] = [];

class FakeEventSource {
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private readonly listeners = new Map<string, (message: { data: string }) => void>();

  readonly url: string;

  constructor(url: string) {
    this.url = url;
    opened.push(this);
  }

  addEventListener(name: string, listener: (message: { data: string }) => void) {
    this.listeners.set(name, listener);
  }
  close() {
    this.closed = true;
  }
  emit(name: string, data = "") {
    this.listeners.get(name)?.({ data });
  }
}

beforeEach(() => {
  opened.length = 0;
  vi.useFakeTimers();
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.resetModules();
});

const frame = (...topics: string[]) => JSON.stringify({ events: topics.map((topic, id) => ({ topic, entity: `e:${id}` })) });

// Six connections per origin over HTTP/1.1: two screens listening must not take two of them.
test("two subscribers share one connection, and the last to leave closes it", async () => {
  const { subscribe } = await import("./eventStream");
  const leaveJobs = subscribe({ topics: ["job"] });
  const leaveAll = subscribe({ topics: [] });
  vi.advanceTimersByTime(250);

  expect(opened.map((source) => [source.url, source.closed])).toEqual([["/api/v1/events", false]]);

  leaveJobs();
  vi.advanceTimersByTime(250);
  expect(opened).toHaveLength(1);

  leaveAll();
  vi.advanceTimersByTime(250);
  expect(opened.map((source) => source.closed)).toEqual([true]);
});

test("the stream asks for the union of the topics, and each subscriber hears only its own", async () => {
  const { subscribe } = await import("./eventStream");
  const jobs = vi.fn();
  const acquisitions = vi.fn();
  subscribe({ topics: ["job"], onEvents: jobs });
  subscribe({ topics: ["acquisition"], onEvents: acquisitions });
  vi.advanceTimersByTime(250);

  expect(opened.map((source) => source.url)).toEqual(["/api/v1/events?topics=acquisition,job"]);

  opened[0]?.emit("update", frame("job", "library"));
  expect(jobs.mock.calls).toEqual([[[{ topic: "job", entity: "e:0" }]]]);
  expect(acquisitions).not.toHaveBeenCalled();
});

test("a screen change that leaves and rejoins at once keeps the connection", async () => {
  const { subscribe } = await import("./eventStream");
  const leave = subscribe({ topics: ["job"] });
  leave();
  subscribe({ topics: ["job"] });
  vi.advanceTimersByTime(250);

  expect(opened.map((source) => source.closed)).toEqual([false]);
});

test("a late subscriber is told the connection is already live", async () => {
  const { subscribe } = await import("./eventStream");
  subscribe({ topics: [] });
  vi.advanceTimersByTime(0);
  opened[0]?.onopen?.();
  const connection = vi.fn();
  subscribe({ topics: [], onConnection: connection });

  expect(connection.mock.calls).toEqual([["live"]]);
});

/** The posts of the fake channel, and a way to deliver one to the tab under test. */
const posted: unknown[] = [];
let deliver: (data: unknown) => void = () => {};

class FakeChannel {
  onmessage: ((message: { data: unknown }) => void) | null = null;
  constructor() {
    deliver = (data) => this.onmessage?.({ data });
  }
  postMessage(data: unknown) {
    posted.push(data);
  }
}

// Many tabs of one browser must not each hold a stream: only the tab holding the leader lock reads it.
test("a tab that does not hold the leader lock opens no stream and takes the leader's frames", async () => {
  posted.length = 0;
  vi.stubGlobal("BroadcastChannel", FakeChannel);
  vi.stubGlobal("navigator", { locks: { request: () => new Promise(() => {}) } });
  const { subscribe } = await import("./eventStream");
  const received: unknown[] = [];
  const connections: string[] = [];
  subscribe({ topics: ["job"], onEvents: (events) => received.push(...events), onConnection: (value) => connections.push(value) });
  vi.advanceTimersByTime(250);

  expect(opened).toHaveLength(0);
  expect(posted).toContainEqual(expect.objectContaining({ kind: "topics", topics: "job" }));

  deliver({ kind: "frame", events: [{ topic: "job", entity: "j:1" }, { topic: "other", entity: "o:1" }] });
  deliver({ kind: "connection", value: "live" });
  expect(received).toEqual([{ topic: "job", entity: "j:1" }]);
  expect(connections.at(-1)).toBe("live");
});

test("the leader reads one stream for every tab's topics and relays what it reads", async () => {
  posted.length = 0;
  vi.stubGlobal("BroadcastChannel", FakeChannel);
  vi.stubGlobal("navigator", { locks: { request: (_name: string, grant: () => Promise<void>) => void grant() } });
  const { subscribe } = await import("./eventStream");
  subscribe({ topics: ["job"] });
  await vi.advanceTimersByTimeAsync(250);
  deliver({ kind: "topics", tab: "other-tab", topics: "queue" });
  await vi.advanceTimersByTimeAsync(250);

  expect(opened.at(-1)?.url).toBe("/api/v1/events?topics=job,queue");
  opened.at(-1)?.emit("update", frame("queue"));
  expect(posted).toContainEqual({ kind: "frame", events: [{ topic: "queue", entity: "e:0" }] });
});
