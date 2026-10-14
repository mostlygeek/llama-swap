/**
 * Emits the two packaging variants of the Tailcat Playground page.
 *
 * Vite has already written a self-contained index.html (every script, style
 * and font folded in) and the Makefile has written the gzipped wasm module
 * beside it. This produces:
 *
 *   index.html + main.wasm.gz  the split pair, for hosting on a web server
 *   llama-swap-tailcat-playground.html  one file, wasm inlined as base64
 *
 * The page picks between them at runtime by looking for the inline element, so
 * both come from one code path in src/tailcat/wasm.ts.
 */

import { readFile, rm, writeFile, stat } from "node:fs/promises";
import path from "node:path";

const WASM_ELEMENT_ID = "tailcat-wasm-gz";
const SINGLE_FILE_NAME = "llama-swap-tailcat-playground.html";

function human(bytes) {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

const dist = process.argv[2];
if (!dist) {
  console.error("usage: build-tailcat.mjs <dist directory>");
  process.exit(1);
}

// Vite names its output after the entry, ui/tailcat.html. The split pair
// wants the conventional index.html, so this is where it gets renamed.
const builtPath = path.join(dist, "tailcat.html");
const htmlPath = path.join(dist, "index.html");
const wasmPath = path.join(dist, "main.wasm.gz");

const html = await readFile(builtPath, "utf8");
const wasm = await readFile(wasmPath);

if (/<script[^>]+\bsrc=/i.test(html) || /<link[^>]+stylesheet/i.test(html)) {
  console.error(`${builtPath} still references sibling assets; the inlining plugin did not run`);
  process.exit(1);
}

// A script element with a non-JavaScript type is inert markup, which is how
// the page carries ~8 MB of base64 without the browser trying to run it.
const blob =
  `<script id="${WASM_ELEMENT_ID}" type="application/octet-stream">` +
  wasm.toString("base64") +
  "</script>";

const single = html.replace("</body>", `${blob}</body>`);
if (single === html) {
  console.error(`${htmlPath} has no </body> to inject the module into`);
  process.exit(1);
}

const singlePath = path.join(dist, SINGLE_FILE_NAME);
await writeFile(singlePath, single);
await writeFile(htmlPath, html);
await rm(builtPath);

console.log(`  ${SINGLE_FILE_NAME}  ${human((await stat(singlePath)).size)}  (single file)`);
console.log(`  index.html            ${human(Buffer.byteLength(html))}  + main.wasm.gz ${human(wasm.length)}  (split pair)`);
