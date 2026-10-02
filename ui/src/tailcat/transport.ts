/**
 * Routes the Playground's requests over Tailcat instead of the network.
 *
 * The Playground's API modules all fetch hardcoded root-relative paths
 * ("/v1/chat/completions", "/sdapi/v1/txt2img", ...) because in the normal UI
 * the page is served by the llama-swap it talks to. Here it is not: the page
 * may be a file:// document, and the node is reachable only through the
 * WireGuard tunnel inside the wasm module.
 *
 * Swapping the global fetch rather than threading a base URL through nine
 * modules is deliberate, and it is the same trade ui/src/cli/fetchBase.ts
 * makes so the Node docs-agent CLI can run those modules unchanged. Here it
 * buys something more: the Playground components need no knowledge that they
 * are talking over Tailcat at all.
 */

import { bridge, type WireRequest } from "./bridge";

/**
 * The synthetic host tunnelled requests carry. internal/config/tailcat.go
 * rewrites `tailcat://<token>` peer URLs to this same host, so a node sees the
 * same Host header from this page as from another llama-swap.
 */
export const TAILCAT_ORIGIN = "http://server.tailcat";

/** Split out so the routing rule can be unit tested without touching globals. */
export function isTailcatRequest(url: string): boolean {
  return url.startsWith("/");
}

function requestUrl(input: RequestInfo | URL): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
}

/**
 * Serializes a fetch call into the bytes-and-headers form the wasm module
 * takes.
 *
 * The browser's own Request does the work rather than a hand-rolled switch
 * over string/Blob/FormData/ArrayBuffer, because only it can generate a
 * multipart body together with the Content-Type naming that body's boundary.
 * /v1/audio/transcriptions and /v1/images/edits both need exactly that.
 */
export async function toWireRequest(
  url: string,
  init?: RequestInit,
  apiKey?: string,
): Promise<WireRequest> {
  const request = new Request(new URL(url, TAILCAT_ORIGIN), init);

  // GET and HEAD cannot carry one, and reading it would be an error.
  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  const buffer = hasBody ? await request.arrayBuffer() : null;

  const headers: [string, string][] = [];
  request.headers.forEach((value, name) => headers.push([name, value]));
  if (apiKey && !request.headers.has("Authorization")) {
    // Bearer is one of the three forms swaputil.ExtractAPIKey accepts, and the
    // one installFetchBase already uses. A caller that set its own
    // Authorization keeps it.
    headers.push(["Authorization", `Bearer ${apiKey}`]);
  }

  return {
    method: request.method,
    url: request.url,
    headers,
    body: buffer && buffer.byteLength > 0 ? new Uint8Array(buffer) : null,
    signal: init?.signal ?? null,
  };
}

/**
 * Installs the wrapper. Returns a function that restores the original fetch.
 *
 * The API key is read through a getter rather than captured so that editing a
 * saved server's key and reconnecting takes effect without reinstalling the
 * global.
 */
export function installTailcatFetch(getApiKey: () => string): () => void {
  const real = globalThis.fetch;

  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = requestUrl(input);
    if (!isTailcatRequest(url)) {
      // data:, blob: and https: URLs are the page's own business: the inlined
      // wasm blob, object URLs the image and speech tabs create, and the DERP
      // map the wasm module fetches.
      return real(input, init);
    }
    return toWireRequest(url, init, getApiKey()).then((request) => bridge().fetch(request));
  }) as typeof fetch;

  return () => {
    globalThis.fetch = real;
  };
}
