import { get } from "svelte/store";
import { describe, expect, it } from "vitest";
import {
  activityRevision,
  handleAPIEventMessage,
  inFlightRequests,
  inflightRequestEntries,
  uiConfig,
} from "./api";

describe("api store event handling", () => {
  it("parses inflight request entries", () => {
    inFlightRequests.set(0);
    inflightRequestEntries.set([]);

    handleAPIEventMessage(
      JSON.stringify({
        type: "inflight",
        data: JSON.stringify({
          operation: "snapshot",
          requests: [
            {
              id: "7",
              timestamp: "2026-07-03T00:00:00Z",
              model: "m1",
              req_path: "/v1/chat/completions",
              method: "POST",
              req_headers: { "User-Agent": "test-agent" },
              remote_ip: "203.0.113.9",
              resp_headers: {},
              resp_bytes: 0,
              elapsed_ms: 125,
              metadata: { source: "test" },
            },
          ],
        }),
      })
    );

    expect(get(inFlightRequests)).toBe(1);
    expect(get(inflightRequestEntries)).toEqual([
      {
        id: "7",
        timestamp: "2026-07-03T00:00:00Z",
        model: "m1",
        req_path: "/v1/chat/completions",
        method: "POST",
        req_headers: { "User-Agent": "test-agent" },
        remote_ip: "203.0.113.9",
        resp_headers: {},
        resp_bytes: 0,
        elapsed_ms: 125,
        client_received_at_ms: expect.any(Number),
        metadata: { source: "test" },
      },
    ]);
  });

  it("upserts and removes inflight entries by id", () => {
    handleAPIEventMessage(JSON.stringify({
      type: "inflight",
      data: JSON.stringify({
        operation: "upsert",
        request: {
          id: "7",
          timestamp: "2026-07-03T00:00:00Z",
          model: "m1",
          req_path: "/v1/chat/completions",
          method: "POST",
          req_headers: {},
          remote_ip: "203.0.113.9",
          resp_headers: { "Content-Type": "text/event-stream" },
          resp_bytes: 42,
          elapsed_ms: 250,
        },
      }),
    }));

    expect(get(inflightRequestEntries)).toHaveLength(1);
    expect(get(inflightRequestEntries)[0].resp_bytes).toBe(42);

    handleAPIEventMessage(JSON.stringify({
      type: "inflight",
      data: JSON.stringify({ operation: "remove", id: "7" }),
    }));
    expect(get(inflightRequestEntries)).toEqual([]);
    expect(get(inFlightRequests)).toBe(0);
  });

  it("parses UI activity configuration", () => {
    handleAPIEventMessage(JSON.stringify({
      type: "uiConfig",
      data: JSON.stringify({ activity: { session_id: ["X-Trace-ID"] } }),
    }));
    expect(get(uiConfig).activity.session_id).toEqual(["X-Trace-ID"]);
  });

  it("increments activity revision for activity events", () => {
    activityRevision.set(0);

    handleAPIEventMessage(
      JSON.stringify({
        type: "activity",
        data: JSON.stringify({ id: 42 }),
      })
    );

    expect(get(activityRevision)).toBe(1);
  });
});
