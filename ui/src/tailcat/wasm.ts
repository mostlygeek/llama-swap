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

/**
 * Reports a load stage. detail is the bytes received so far while fetching,
 * as "2.1 of 6.0 MB", and empty otherwise.
 */
export type LoadProgress = (stage: LoadStage, detail: string) => void;

function megabytes(n: number): string {
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

/**
 * Wraps body so every chunk that passes updates the fetch progress, and the
 * end of the stream marks the move to decompressing. The bytes are pulled
 * through gunzip as they arrive, so this is the only place the boundary
 * between the two stages is observable.
 */
function counting(body: ReadableStream<Uint8Array>, total: number, onProgress: LoadProgress) {
  let received = 0;
  return body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, controller) {
        received += chunk.byteLength;
        onProgress("fetching", total ? `${megabytes(received)} of ${megabytes(total)}` : megabytes(received));
        controller.enqueue(chunk);
      },
      flush() {
        onProgress("decompressing", "");
      },
    }),
  );
}

let started: Promise<void> | undefined;

function installGoRuntime(): void {
  if ("Go" in globalThis) return;
  // wasm_exec.js is the Go toolchain's own support file, copied out of GOROOT
  // at build time. It is a classic script that assigns globalThis.Go, and it
  // carries Node-only branches that a bundler should not try to resolve, so it
  // is evaluated as source rather than imported as a module.
  new Function(wasmExecSource)();
}

async function moduleBytes(onProgress: LoadProgress): Promise<ArrayBuffer> {
  const inline = document.getElementById(INLINE_ELEMENT_ID);
  // Browsers refuse fetch() of a sibling file on file://, so the split pair
  // only works from a web server. Say so, instead of a bare "Failed to fetch".
  if (!inline && location.protocol === "file:") {
    throw new Error(
      "This copy of the page loads main.wasm.gz from beside it, which browsers do not allow " +
        "from file://. Serve index.html and main.wasm.gz from a web server, or open " +
        "llama-swap-tailcat-playground.html, which has the module built in.",
    );
  }
  const base64 = inline ? (inline.textContent ?? "").trim() : "";
  const source = inline ? `data:application/octet-stream;base64,${base64}` : SIDECAR_URL;

  onProgress("fetching", "");
  const response = await fetch(source);
  if (!response.ok || !response.body) {
    throw new Error(`could not load the Tailcat module (${response.status})`);
  }
  // A data: response carries no Content-Length; its size follows from the base64.
  const total = inline
    ? Math.floor((base64.length * 3) / 4)
    : Number(response.headers.get("content-length") ?? 0);

  const decompressed = counting(response.body, total, onProgress).pipeThrough(
    new DecompressionStream("gzip"),
  );
  return await new Response(decompressed).arrayBuffer();
}

/**
 * Loads, instantiates and starts the module, resolving once it has installed
 * its globals. Repeat calls share the first load.
 */
export function startTailcatWasm(onProgress: LoadProgress = () => {}): Promise<void> {
  started ??= (async () => {
    installGoRuntime();
    const bytes = await moduleBytes(onProgress);

    onProgress("compiling", "");
    const go = new Go();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);

    onProgress("starting", "");
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
