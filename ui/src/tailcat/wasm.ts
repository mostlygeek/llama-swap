/**
 * Loads the js/wasm Tailcat module.
 *
 * Two packaging variants share this one path. The single-file build inlines
 * the gzipped module as base64 in a script element; the split build leaves it
 * beside the page as main.wasm.gz. Either way the bytes arrive gzipped and are
 * decompressed by the browser, because the module is ~27 MB raw and ~6 MB
 * compressed.
 *
 * The base64 goes through fetch() of a data: URL rather than atob(), so an
 * ~8 MB string is streamed into the decompressor instead of being turned into
 * a second ~27 MB JavaScript string first.
 */

import wasmExecSource from "./generated/wasm_exec.js?raw";

/** Set by wasm_exec.js. */
declare const Go: { new (): { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): void } };

const INLINE_ELEMENT_ID = "tailcat-wasm-gz";
const SIDECAR_URL = "./main.wasm.gz";

export type LoadStage = "fetching" | "decompressing" | "compiling" | "starting";

let started: Promise<void> | undefined;

function installGoRuntime(): void {
  if ("Go" in globalThis) return;
  // wasm_exec.js is the Go toolchain's own support file, copied out of GOROOT
  // at build time. It is a classic script that assigns globalThis.Go, and it
  // carries Node-only branches that a bundler should not try to resolve, so it
  // is evaluated as source rather than imported as a module.
  new Function(wasmExecSource)();
}

async function moduleBytes(onStage: (stage: LoadStage) => void): Promise<ArrayBuffer> {
  const inline = document.getElementById(INLINE_ELEMENT_ID);
  const source = inline
    ? `data:application/octet-stream;base64,${(inline.textContent ?? "").trim()}`
    : SIDECAR_URL;

  onStage("fetching");
  const response = await fetch(source);
  if (!response.ok || !response.body) {
    throw new Error(`could not load the Tailcat module (${response.status})`);
  }

  onStage("decompressing");
  const decompressed = response.body.pipeThrough(new DecompressionStream("gzip"));
  return await new Response(decompressed).arrayBuffer();
}

/**
 * Loads, instantiates and starts the module, resolving once it has installed
 * its globals. Repeat calls share the first load.
 */
export function startTailcatWasm(onStage: (stage: LoadStage) => void = () => {}): Promise<void> {
  started ??= (async () => {
    installGoRuntime();
    const bytes = await moduleBytes(onStage);

    onStage("compiling");
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);

    onStage("starting");
    const ready = new Promise<void>((resolve) => {
      globalThis.onLlamaSwapTailcatReady = resolve;
    });
    // main() ends in select{}, so run() only settles when the module exits.
    void go.run(instance);
    await ready;
  })().catch((error: unknown) => {
    // A failed load must not poison every later attempt, so the user can retry.
    started = undefined;
    throw error;
  });
  return started;
}
