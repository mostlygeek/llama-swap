import { get } from "svelte/store";
import { describe, expect, it } from "vitest";
import {
  activityRevision,
  handleAPIEventMessage,
  inFlightRequests,
  inflightRequestEntries,
} from "./api";

describe("api store event handling", () => {
  it("parses inflight request entries", () => {
    inFlightRequests.set(0);
    inflightRequestEntries.set([]);

    handleAPIEventMessage(
      JSON.stringify({
        type: "inflight",
        data: JSON.stringify({
          requests: [
            {
              id: "7",
              timestamp: "2026-07-03T00:00:00Z",
              model: "m1",
              req_path: "/v1/chat/completions",
              method: "POST",
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
        metadata: { source: "test" },
      },
    ]);
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
