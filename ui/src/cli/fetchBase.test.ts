import { describe, it, expect, vi, afterEach } from "vitest";
import { resolveUrl, installFetchBase } from "./fetchBase";

describe("resolveUrl", () => {
  it("resolves a relative path against the base", () => {
    expect(resolveUrl("/v1/chat/completions", "http://host:8080")).toBe("http://host:8080/v1/chat/completions");
    expect(resolveUrl("/api/mcp", "http://host:8080")).toBe("http://host:8080/api/mcp");
  });

  it("keeps a base path prefix", () => {
    expect(resolveUrl("/api/mcp", "http://host:8080/swap/")).toBe("http://host:8080/api/mcp");
  });

  it("leaves absolute URLs alone", () => {
    expect(resolveUrl("https://elsewhere/x", "http://host:8080")).toBe("https://elsewhere/x");
  });
});

describe("installFetchBase", () => {
  let restore: (() => void) | undefined;
  afterEach(() => {
    restore?.();
    restore = undefined;
  });

  it("rewrites relative URLs and adds the auth header", async () => {
    const real = vi.fn().mockResolvedValue(new Response("ok"));
    vi.stubGlobal("fetch", real);

    restore = installFetchBase({ baseUrl: "http://host:8080", apiKey: "secret" });
    await fetch("/api/mcp", { method: "POST" });

    const [url, init] = real.mock.calls[0];
    expect(url).toBe("http://host:8080/api/mcp");
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer secret");
  });

  it("omits the auth header when no key is given", async () => {
    const real = vi.fn().mockResolvedValue(new Response("ok"));
    vi.stubGlobal("fetch", real);

    restore = installFetchBase({ baseUrl: "http://host:8080" });
    await fetch("/v1/chat/completions", { method: "POST" });

    const [, init] = real.mock.calls[0];
    expect(new Headers(init.headers).get("Authorization")).toBeNull();
  });

  it("passes absolute URLs through untouched", async () => {
    const real = vi.fn().mockResolvedValue(new Response("ok"));
    vi.stubGlobal("fetch", real);

    restore = installFetchBase({ baseUrl: "http://host:8080", apiKey: "secret" });
    await fetch("https://elsewhere/x");

    expect(real.mock.calls[0][0]).toBe("https://elsewhere/x");
  });

  it("restores the original fetch", () => {
    const real = vi.fn();
    vi.stubGlobal("fetch", real);
    const undo = installFetchBase({ baseUrl: "http://host:8080" });
    expect(globalThis.fetch).not.toBe(real);
    undo();
    expect(globalThis.fetch).toBe(real);
  });
});
