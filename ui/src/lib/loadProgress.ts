import type { Model } from "./types";
import { formatDuration } from "./format";

export type LoadProgressMode = "indeterminate" | "estimated" | "overrun";

export interface LoadProgress {
  mode: LoadProgressMode;
  elapsedMs: number;
  /** 0-95 while estimated/overrun; null when indeterminate. */
  pct: number | null;
  /** Remaining ms while estimated; 0 when overrun; undefined when indeterminate. */
  etaMs?: number;
}

export const LOAD_PROGRESS_MAX_PCT = 95;

/** Returns null unless the model is starting. Pure; `nowMs` is injected for testability. */
export function computeLoadProgress(
  model: Pick<Model, "state" | "loadStartedAt" | "estLoadMs">,
  nowMs: number,
): LoadProgress | null {
  if (model.state !== "starting") {
    return null;
  }

  const elapsedMs = Math.max(0, nowMs - (model.loadStartedAt ?? nowMs));

  if (!model.estLoadMs || model.estLoadMs <= 0) {
    return { mode: "indeterminate", elapsedMs, pct: null };
  }

  const ratio = elapsedMs / model.estLoadMs;

  if (ratio < 1) {
    return {
      mode: "estimated",
      elapsedMs,
      pct: Math.min(LOAD_PROGRESS_MAX_PCT, Math.round(ratio * 100)),
      etaMs: model.estLoadMs - elapsedMs,
    };
  }

  return {
    mode: "overrun",
    elapsedMs,
    pct: LOAD_PROGRESS_MAX_PCT,
    etaMs: 0,
  };
}

/**
 * Formats remaining time as a coarse "~Ns left". Seconds are rounded *up* (and
 * floored at 1s) so the label never promises the load sooner than the estimate
 * does, and never shows a "~0s left" beat just before the estimate is crossed.
 */
export function formatRemainingLabel(etaMs: number): string {
  const wholeSeconds = Math.max(1, Math.ceil(Math.max(0, etaMs) / 1000));
  return `~${formatDuration(wholeSeconds * 1000, { precision: 0 })} left`;
}

/** Progress line shown under the load bar: elapsed at 0.1s, plus time remaining when known. */
export function formatLoadProgressLabel(p: LoadProgress): string {
  const elapsed = formatDuration(p.elapsedMs, { precision: 1 });
  switch (p.mode) {
    case "indeterminate":
      return `Loading… ${elapsed}`;
    case "estimated":
      return `Loading… ${elapsed} · ${formatRemainingLabel(p.etaMs ?? 0)}`;
    case "overrun":
      return `Taking longer than usual… ${elapsed}`;
  }
}
