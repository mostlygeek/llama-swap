import { describe, expect, it } from "vitest";
import { playgroundSessionHeaders, playgroundSessionID } from "./playgroundSession";

describe("playground session", () => {
  it("creates one five-character Playground session id", () => {
    expect(playgroundSessionID).toMatch(/^lspg-[a-z0-9]{5}$/);
    expect(playgroundSessionHeaders["X-Session-ID"]).toBe(playgroundSessionID);
  });
});
