import type { Model } from "./types";
import { computeLoadProgress, LOAD_PROGRESS_MAX_PCT } from "./loadProgress";

/**
 * Visual state of a model status dot. `filling` and `overrun` carry a fill
 * percentage; every other kind is a flat colour chosen by the component.
 */
export type StatusDotKind = "stopped" | "stopping" | "indeterminate" | "filling" | "overrun" | "ready";

export interface StatusDotAppearance {
  kind: StatusDotKind;
  /** Fill percentage (0-95) for `filling`/`overrun`; null for flat-colour kinds. */
  pct: number | null;
}

/**
 * Maps a model's state plus live load progress to a dot appearance. Pure;
 * `nowMs` is injected for testability. A starting model with no estimate
 * (including peers, which never carry load timing) is `indeterminate`.
 */
export function statusDotAppearance(
  model: Pick<Model, "state" | "loadStartedAt" | "estLoadMs">,
  nowMs: number,
): StatusDotAppearance {
  switch (model.state) {
    case "ready":
      return { kind: "ready", pct: null };
    case "stopping":
      return { kind: "stopping", pct: null };
    case "starting": {
      const progress = computeLoadProgress(model, nowMs);
      if (!progress || progress.pct === null) {
        return { kind: "indeterminate", pct: null };
      }
      return { kind: progress.mode === "overrun" ? "overrun" : "filling", pct: progress.pct };
    }
    default:
      return { kind: "stopped", pct: null };
  }
}

function clampPct(pct: number): number {
  return Math.min(LOAD_PROGRESS_MAX_PCT, Math.max(0, Math.round(pct)));
}

/**
 * Colour of the filled pie sector: pure `--warning` at 0%, blending toward
 * `--success` as the load approaches its estimate, so the dot "warms" toward
 * the solid green it snaps to on ready. Uses the raw theme variables rather
 * than Tailwind's `--color-*` aliases, which `@theme inline` may not emit.
 */
export function loadFillColor(pct: number): string {
  return `color-mix(in oklch, var(--warning), var(--success) ${clampPct(pct)}%)`;
}

/**
 * Floor, in percent, applied to the *label text* colour ramp. The bar and dot
 * paint pure `--warning` at 0%, but on a light background that bright amber
 * (L≈0.77) is low-contrast as small text, so the label starts partway along the
 * ramp — `--success` is darker (L≈0.6) and greener, so a higher floor both
 * darkens and de-yellows the early text. The label still drifts to near-green
 * as the load completes; it just never sits at the least-legible amber.
 */
export const LOAD_LABEL_MIN_SUCCESS_PCT = 40;

/**
 * Colour for the load-progress label: the same warm→green ramp as
 * {@link loadFillColor}, but floored at {@link LOAD_LABEL_MIN_SUCCESS_PCT} so
 * the text stays legible while the bar keeps the fuller 0→95 amber sweep.
 */
export function loadLabelColor(pct: number): string {
  return loadFillColor(Math.max(LOAD_LABEL_MIN_SUCCESS_PCT, pct));
}

/**
 * Colour of the animated "loading" ring drawn around the dot, or "" when the
 * model is not loading (the caller then renders no ring). The ring is the
 * unified loading affordance across all three starting sub-states, so the dot's
 * interior no longer needs its own pulse. `filling` tracks the pie's warm→green
 * hue; the flat-amber states (`indeterminate`, `overrun`) ring in plain warning
 * to match their interior.
 */
export function statusDotRingColor(appearance: StatusDotAppearance): string {
  switch (appearance.kind) {
    case "filling":
      return appearance.pct === null ? "var(--warning)" : loadFillColor(appearance.pct);
    case "indeterminate":
    case "overrun":
      return "var(--warning)";
    default:
      return "";
  }
}

/**
 * Inline style for the dot. Filling kinds paint a conic pie (clockwise from
 * 12 o'clock) over the component's grey track; the transparent remainder lets
 * the track show through as the unfilled portion. Overrun keeps the sector in
 * plain warning amber: the estimate has been passed, so the near-green hue
 * would falsely suggest "almost done".
 */
export function statusDotStyle(appearance: StatusDotAppearance): string {
  if (appearance.pct === null) {
    return "";
  }
  const pct = clampPct(appearance.pct);
  const color = appearance.kind === "overrun" ? "var(--warning)" : loadFillColor(pct);
  return `background-image: conic-gradient(${color} ${pct}%, transparent 0)`;
}
