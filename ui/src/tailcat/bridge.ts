/**
 * Typed view of the globals the js/wasm module installs.
 *
 * See cmd/tailcat-playground-wasm/main_js.go for the other side of each of
 * these. The module sets globalThis.llamaSwapTailcat once it has started, and
 * calls globalThis.onLlamaSwapTailcatReady if the page defined one.
 */

export interface TailcatIdentity {
  /** Canonical "nodekey:..." form, ready to paste into a server's tailcat.allow. */
  nodeKey: string;
  /** The key to persist; pass it back to identity() and connect(). */
  privateKeyJSON: string;
  /** True when a saved key could not be read and a fresh one replaced it. */
  regenerated: boolean;
}

export interface ConnectOptions {
  token: string;
  privateKeyJSON: string;
  derpMapURL: string;
  verbose: boolean;
}

/** The request shape doFetch() expects. Headers are pairs so repeats survive. */
export interface WireRequest {
  method: string;
  url: string;
  headers: [string, string][];
  body: Uint8Array | null;
  signal?: AbortSignal | null;
}

export interface TailcatBridge {
  identity(privateKeyJSON: string | null): TailcatIdentity;
  connect(options: ConnectOptions): Promise<{ nodeKey: string }>;
  fetch(request: WireRequest): Promise<Response>;
  disconnect(): void;
}

declare global {
  // eslint-disable-next-line no-var
  var llamaSwapTailcat: TailcatBridge | undefined;
  // eslint-disable-next-line no-var
  var onLlamaSwapTailcatReady: (() => void) | undefined;
}

export function bridge(): TailcatBridge {
  const value = globalThis.llamaSwapTailcat;
  if (!value) {
    throw new Error("the Tailcat WebAssembly module has not finished loading");
  }
  return value;
}
