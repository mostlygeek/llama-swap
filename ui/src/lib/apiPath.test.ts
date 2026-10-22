import { describe, it, expect } from "vitest";
import { uiPrefix, apiUrl } from "./apiPath";

describe("apiPath", () => {
  it("returns no prefix for the default root mount", () => {
    expect(uiPrefix("/ui/")).toBe("");
    expect(uiPrefix("/ui")).toBe("");
  });

  it("returns the prefix the proxy mounted the UI under", () => {
    expect(uiPrefix("/asubpath/llama-swap/ui/")).toBe("/asubpath/llama-swap");
    expect(uiPrefix("/asubpath/llama-swap/ui")).toBe("/asubpath/llama-swap");
    expect(uiPrefix("/llama-swap/ui/")).toBe("/llama-swap");
  });

  it("returns no prefix when the path is not the UI mount", () => {
    expect(uiPrefix("/")).toBe("");
    expect(uiPrefix("/ui/models/foo")).toBe("");
  });

  it("leaves paths unchanged outside the browser", () => {
    // Vitest runs in a Node environment: no window, rooted paths pass
    // through so cli/fetchBase.ts can resolve them against --base-url.
    expect(apiUrl("/api/mcp")).toBe("/api/mcp");
  });
});
