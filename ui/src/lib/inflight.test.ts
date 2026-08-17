import { describe, expect, it } from "vitest";
import { formatBytes, liveElapsedMs, requestHeader, sessionID } from "./inflight";

describe("inflight helpers", () => {
  it("looks up headers case-insensitively", () => {
    expect(requestHeader({ "User-Agent": "agent" }, "user-agent")).toBe("agent");
  });

  it("uses configured session header precedence", () => {
    const headers = { "X-Litellm-Session-Id": "second", "X-Session-Id": "first" };
    expect(sessionID(headers, ["x-session-id", "x-litellm-session-id"])).toBe("first");
  });

  it("formats byte counts", () => {
    expect(formatBytes(42)).toBe("42 B");
    expect(formatBytes(2048)).toBe("2.00 KB");
  });

  it("advances server elapsed time from a client-local receipt time", () => {
    expect(liveElapsedMs(250, 1_000, 2_500)).toBe(1_750);
  });
});
