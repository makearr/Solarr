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
