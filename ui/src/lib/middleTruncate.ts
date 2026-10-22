const ELLIPSIS = "…";
const SEPARATORS = "-_./:";

/**
 * Where the always-visible tail of `text` starts: the last `minTail`
 * characters, moved back to just after a separator when one is a few
 * characters earlier, so "qwen36-35b-a3b-q8_0" keeps "35b-a3b-q8_0" rather
 * than cutting into "b-a3b-q8_0".
 */
export function tailStart(text: string, minTail: number): number {
  const start = Math.max(0, text.length - Math.min(minTail, Math.floor(text.length / 2)));
  for (let i = start - 1; i >= Math.max(1, start - 4); i--) {
    if (SEPARATORS.includes(text[i])) return i + 1;
  }
  return start;
}

/**
 * Shortens `text` to fit `maxWidth` by replacing its middle with an ellipsis,
 * keeping as much of the start as fits and at least the tail picked by
 * tailStart. `measure` returns the rendered width of a string.
 */
export function middleTruncate(
  text: string,
  maxWidth: number,
  measure: (s: string) => number,
  minTail = 10,
): string {
  if (maxWidth <= 0 || measure(text) <= maxWidth) return text;

  const tail = text.slice(tailStart(text, minTail));
  const fits = (head: number) => measure(text.slice(0, head) + ELLIPSIS + tail) <= maxWidth;

  // Longest head that still fits alongside the tail.
  let lo = 0;
  let hi = text.length - tail.length;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (fits(mid)) lo = mid;
    else hi = mid - 1;
  }
  if (lo > 0 || fits(0)) return text.slice(0, lo) + ELLIPSIS + tail;

  // Too narrow for even the tail: keep the longest suffix that fits.
  for (let i = 1; i < text.length; i++) {
    const s = ELLIPSIS + text.slice(i);
    if (measure(s) <= maxWidth) return s;
  }
  return ELLIPSIS;
}
