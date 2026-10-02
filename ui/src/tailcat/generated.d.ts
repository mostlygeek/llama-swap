/**
 * Types for the Go toolchain's wasm_exec.js, which `make tailcat-playground`
 * copies out of GOROOT into src/tailcat/generated/.
 *
 * The declaration is here rather than relying on vite/client because the file
 * is generated: `npm run check` runs on a clean checkout where it does not
 * exist yet, and a wildcard module resolves without one.
 */
declare module "*?raw" {
  const source: string;
  export default source;
}
