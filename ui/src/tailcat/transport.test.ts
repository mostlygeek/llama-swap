import { describe, it, expect, vi, afterEach } from "vitest";
import { isTailcatRequest, toWireRequest, installTailcatFetch, TAILCAT_ORIGIN } from "./transport";

function headerMap(headers: [string, string][]): Record<string, string> {
  return Object.fromEntries(headers.map(([name, value]) => [name.toLowerCase(), value]));
}

describe("isTailcatRequest", () => {
  it("claims the root-relative paths the Playground uses", () => {
    expect(isTailcatRequest("/v1/chat/completions")).toBe(true);
    expect(isTailcatRequest("/sdapi/v1/txt2img")).toBe(true);
  });

  it("leaves everything else to the real fetch", () => {
    expect(isTailcatRequest("https://tailcat.dev/derpmap.json")).toBe(false);
    expect(isTailcatRequest("blob:null/abc")).toBe(false);
    expect(isTailcatRequest("data:application/octet-stream;base64,AAA")).toBe(false);
  });
});

describe("toWireRequest", () => {
  it("resolves the path against the tunnel's synthetic origin", async () => {
    const request = await toWireRequest("/v1/models");
    expect(request.url).toBe(`${TAILCAT_ORIGIN}/v1/models`);
    expect(request.method).toBe("GET");
    expect(request.body).toBeNull();
  });

  it("carries a JSON body through as bytes", async () => {
    const request = await toWireRequest("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Session-ID": "lspg-1" },
      body: JSON.stringify({ model: "chat" }),
    });

    expect(request.method).toBe("POST");
    expect(new TextDecoder().decode(request.body!)).toBe('{"model":"chat"}');
    const headers = headerMap(request.headers);
    expect(headers["content-type"]).toBe("application/json");
    expect(headers["x-session-id"]).toBe("lspg-1");
  });

  // The multipart boundary is why the browser's Request does the serialization
  // rather than a hand-rolled switch: /v1/audio/transcriptions is unusable
  // without a Content-Type that names the boundary in the body.
  it("lets the browser generate the multipart boundary", async () => {
    const form = new FormData();
    form.append("model", "whisper");
    form.append("file", new Blob(["audio"], { type: "audio/wav" }), "clip.wav");

    const request = await toWireRequest("/v1/audio/transcriptions", { method: "POST", body: form });

    const contentType = headerMap(request.headers)["content-type"];
    expect(contentType).toMatch(/^multipart\/form-data; boundary=.+/);

    const boundary = contentType.split("boundary=")[1];
    const body = new TextDecoder().decode(request.body!);
    expect(body).toContain(boundary);
    expect(body).toContain('name="model"');
    expect(body).toContain("clip.wav");
  });

  it("adds the API key as a bearer token", async () => {
    const request = await toWireRequest("/v1/models", undefined, "secret");
    expect(headerMap(request.headers)["authorization"]).toBe("Bearer secret");
  });

  it("omits the header when no key is configured", async () => {
    const request = await toWireRequest("/v1/models", undefined, "");
    expect(headerMap(request.headers)["authorization"]).toBeUndefined();
  });

  it("does not overwrite an Authorization the caller set", async () => {
    const request = await toWireRequest("/v1/models", { headers: { Authorization: "Bearer caller" } }, "secret");
    const values = request.headers.filter(([name]) => name.toLowerCase() === "authorization");
    expect(values).toEqual([["authorization", "Bearer caller"]]);
  });

  it("keeps the query string a model-dispatched GET depends on", async () => {
    const request = await toWireRequest("/v1/audio/voices?model=kokoro");
    expect(request.url).toBe(`${TAILCAT_ORIGIN}/v1/audio/voices?model=kokoro`);
  });
});

describe("installTailcatFetch", () => {
  let restore: (() => void) | undefined;
  afterEach(() => {
    restore?.();
    restore = undefined;
    vi.unstubAllGlobals();
  });

  it("sends root-relative requests over the bridge", async () => {
    const bridgeFetch = vi.fn().mockResolvedValue(new Response("ok"));
    const real = vi.fn().mockResolvedValue(new Response("real"));
    vi.stubGlobal("fetch", real);
    vi.stubGlobal("llamaSwapTailcat", { fetch: bridgeFetch });

    restore = installTailcatFetch(() => "secret");
    await fetch("/v1/models");

    expect(real).not.toHaveBeenCalled();
    const [request] = bridgeFetch.mock.calls[0];
    expect(request.url).toBe(`${TAILCAT_ORIGIN}/v1/models`);
    expect(headerMap(request.headers)["authorization"]).toBe("Bearer secret");
  });

  it("passes absolute URLs to the real fetch", async () => {
    const bridgeFetch = vi.fn();
    const real = vi.fn().mockResolvedValue(new Response("real"));
    vi.stubGlobal("fetch", real);
    vi.stubGlobal("llamaSwapTailcat", { fetch: bridgeFetch });

    restore = installTailcatFetch(() => "secret");
    await fetch("https://tailcat.dev/derpmap.json");

    expect(bridgeFetch).not.toHaveBeenCalled();
    expect(real.mock.calls[0][0]).toBe("https://tailcat.dev/derpmap.json");
  });

  // The key is read per request so editing a saved server takes effect without
  // reinstalling the global.
  it("reads the API key at request time", async () => {
    const bridgeFetch = vi.fn().mockResolvedValue(new Response("ok"));
    vi.stubGlobal("fetch", vi.fn());
    vi.stubGlobal("llamaSwapTailcat", { fetch: bridgeFetch });

    let key = "first";
    restore = installTailcatFetch(() => key);
    await fetch("/v1/models");
    key = "second";
    await fetch("/v1/models");

    expect(headerMap(bridgeFetch.mock.calls[0][0].headers)["authorization"]).toBe("Bearer first");
    expect(headerMap(bridgeFetch.mock.calls[1][0].headers)["authorization"]).toBe("Bearer second");
  });

  it("restores the original fetch", () => {
    const real = vi.fn();
    vi.stubGlobal("fetch", real);
    const undo = installTailcatFetch(() => "");
    expect(globalThis.fetch).not.toBe(real);
    undo();
    expect(globalThis.fetch).toBe(real);
  });
});
