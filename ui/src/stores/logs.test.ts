import { get } from "svelte/store";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connectLogStreams, handleLogEventMessage, httpLogs, proxyLogs, upstreamLogs } from "./logs";

function logMessage(source: string, data: string): string {
  return JSON.stringify({ type: "logData", data: JSON.stringify({ source, data }) });
}

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  close() {
    this.closed = true;
  }
}

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  proxyLogs.set("");
  upstreamLogs.set("");
  httpLogs.set("");
});

describe("log streams", () => {
  it("routes log data to the store for its source", () => {
    handleLogEventMessage(logMessage("proxy", "p1\n"));
    handleLogEventMessage(logMessage("upstream", "u1\n"));
    handleLogEventMessage(logMessage("http", "h1\n"));
    handleLogEventMessage(logMessage("http", "h2\n"));

    expect(get(proxyLogs)).toBe("p1\n");
    expect(get(upstreamLogs)).toBe("u1\n");
    expect(get(httpLogs)).toBe("h1\nh2\n");
  });

  it("keeps only the newest 100KB of a stream", () => {
    handleLogEventMessage(logMessage("http", "a".repeat(100 * 1024)));
    handleLogEventMessage(logMessage("http", "END"));

    const log = get(httpLogs);
    expect(log.length).toBe(100 * 1024);
    expect(log.endsWith("END")).toBe(true);
  });

  it("connects only to the requested streams and clears them on close", () => {
    const close = connectLogStreams(["proxy", "http"]);
    expect(FakeEventSource.instances).toHaveLength(1);
    const source = FakeEventSource.instances[0];
    expect(source.url).toBe("/api/events/logs?stream=proxy&stream=http");

    upstreamLogs.set("untouched");
    proxyLogs.set("stale");
    source.onopen?.();
    expect(get(proxyLogs)).toBe("");

    source.onmessage?.({ data: logMessage("proxy", "fresh") } as MessageEvent);
    expect(get(proxyLogs)).toBe("fresh");

    close();
    expect(source.closed).toBe(true);
    expect(get(proxyLogs)).toBe("");
    expect(get(upstreamLogs)).toBe("untouched");
  });

  it("reconnects after an error until closed", () => {
    vi.useFakeTimers();
    const close = connectLogStreams(["upstream"]);
    FakeEventSource.instances[0].onerror?.();

    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.instances).toHaveLength(2);

    close();
    FakeEventSource.instances[1].onerror?.();
    vi.advanceTimersByTime(10000);
    expect(FakeEventSource.instances).toHaveLength(2);
  });
});
