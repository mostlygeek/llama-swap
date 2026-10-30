// Client-side PNG export for a rendered DOM table. The element is rasterized
// in the browser (SVG foreignObject → canvas), so nothing leaves the page and
// no backend endpoint is required.

import { toBlob } from "html-to-image";

/** Default scale factor, matching a retina display for a crisp image. */
export const DEFAULT_PNG_PIXEL_DENSITY = 2;

/** A computed background-color is transparent if it has no opaque paint. */
function isTransparent(color: string): boolean {
  return color === "transparent" || color === "rgba(0, 0, 0, 0)";
}

/**
 * The background to render behind a transparent element: the nearest opaque
 * background walking up the DOM, so the image matches the element's on-screen
 * context (a light card in light mode, a dark card in dark mode). Without
 * this a light-mode-forced background hides light (dark-mode) text.
 */
function resolveBackgroundColor(element: HTMLElement): string {
  let node: HTMLElement | null = element;
  while (node) {
    const background = getComputedStyle(node).backgroundColor;
    if (!isTransparent(background)) return background;
    node = node.parentElement;
  }
  const root = getComputedStyle(document.documentElement).backgroundColor;
  return isTransparent(root) ? "white" : root;
}

/**
 * Rasterize `element` into an image/png Blob.
 *
 * The capture is sized from the element's own bounding box, so a `<table>` is
 * captured at its full (unclipped) height even when a parent scrolls it.
 * pixelRatio scales the output resolution; 2 yields a retina-dense image.
 * The background is taken from the element's nearest opaque ancestor so the
 * result matches the current light/dark theme.
 */
export async function elementToPngBlob(
  element: HTMLElement,
  pixelRatio: number = DEFAULT_PNG_PIXEL_DENSITY
): Promise<Blob> {
  const blob = await toBlob(element, {
    pixelRatio,
    backgroundColor: resolveBackgroundColor(element),
  });
  if (!blob) throw new Error("Failed to rasterize table");
  return blob;
}

/**
 * Copy a PNG Blob to the clipboard as an image. Requires a secure context and
 * a browser that supports ClipboardItem; returns false (rather than throwing)
 * so the caller can surface a fallback message.
 */
export async function copyPngToClipboard(blob: Blob): Promise<boolean> {
  if (!navigator.clipboard || !("ClipboardItem" in window)) return false;
  try {
    await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
    return true;
  } catch (err) {
    console.error("Failed to copy image:", err);
    return false;
  }
}

/** Trigger a browser download of a PNG Blob. */
export function downloadPngBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

/** Convenience: rasterize an element and download it as a PNG. */
export async function downloadElementPng(
  element: HTMLElement,
  filename: string,
  pixelRatio: number = DEFAULT_PNG_PIXEL_DENSITY
): Promise<void> {
  const blob = await elementToPngBlob(element, pixelRatio);
  downloadPngBlob(blob, filename);
}
