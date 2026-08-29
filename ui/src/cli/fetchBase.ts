/**
 * Makes the browser's API modules work in Node.
 *
 * chatApi.ts builds `"/" + endpoint` and agentTools.ts hardcodes
 * MCP_ENDPOINT = "/api/mcp". Both are correct in a browser, where the page is
 * served by the same llama-swap they talk to, and both are unusable under
 * Node's fetch, which rejects relative URLs. Neither module can attach an
 * Authorization header either, for the same same-origin reason.
 *
 * Wrapping the global rather than threading a baseUrl parameter through
 * chatApi.ts and agentTools.ts is the point of this file: the eval has to
 * exercise the code the browser actually runs, byte for byte, or a measured
 * improvement might not be an improvement to the product. The cost is one
 * process-global mutation, which is acceptable in a CLI and nowhere else.
 * agentTools.test.ts already stubs fetch this way via vi.stubGlobal.
 */

/** Split out so the routing rule can be unit tested without touching globals. */
export function resolveUrl(url: string, baseUrl: string): string {
  if (!url.startsWith("/")) return url;
  return new URL(url, baseUrl).toString();
}

export interface FetchBaseOptions {
  baseUrl: string;
  apiKey?: string;
}

/** Installs the wrapper. Returns a function that restores the original fetch. */
export function installFetchBase(opts: FetchBaseOptions): () => void {
  const real = globalThis.fetch;

  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (!url.startsWith("/")) {
      return real(input, init);
    }
    const headers = new Headers(init?.headers);
    if (opts.apiKey) {
      headers.set("Authorization", `Bearer ${opts.apiKey}`);
    }
    return real(resolveUrl(url, opts.baseUrl), { ...init, headers });
  }) as typeof fetch;

  return () => {
    globalThis.fetch = real;
  };
}
