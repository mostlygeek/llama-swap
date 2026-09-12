<script lang="ts">
  import type { Model } from "$lib/types";
  import { computeLoadProgress, formatLoadProgressLabel } from "$lib/loadProgress";
  import { formatDuration } from "$lib/format";
  import { loadFillColor, loadLabelColor } from "$lib/statusDot";

  let { model, className = "" } = $props<{ model: Model; className?: string }>();

  let now = $state(Date.now());
  // Seeded undefined (not model.state) so we don't read the prop non-reactively
  // at init; the transition effect below records the real state on its first run
  // before it can ever match starting→ready, so nothing spurious fires.
  let prevState = $state<Model["state"] | undefined>(undefined);
  let settled = $state(false);
  let loadDurationMs = $state<number | null>(null);
  // The last elapsed observed while starting. The server clears loadStartedAt
  // to 0 (dropped by `omitempty`, so it arrives as undefined) *before* it
  // publishes the ready snapshot, so by the starting→ready transition
  // model.loadStartedAt is already gone. Capturing elapsed live here is the
  // only way for the done-hold to report the real load duration.
  let lastElapsedMs = $state(0);

  // Timer effect: tick while starting, and remember the running elapsed so the
  // done-hold can report it after loadStartedAt has been cleared.
  $effect(() => {
    if (model.state !== "starting") {
      return;
    }

    const tick = () => {
      now = Date.now();
      lastElapsedMs = Math.max(0, now - (model.loadStartedAt ?? now));
    };
    tick(); // seed immediately so a sub-250ms load still records a duration

    const id = setInterval(tick, 250);

    return () => {
      clearInterval(id);
    };
  });

  // Detect the starting→ready transition exactly once. prevState is advanced on
  // *every* run (not only the fallthrough), so a re-delivered ready snapshot —
  // the store replaces every model object on each SSE tick — cannot re-trigger
  // the done-hold and re-flash the bar.
  $effect(() => {
    const wasStarting = prevState === "starting";
    prevState = model.state;
    if (wasStarting && model.state === "ready") {
      // Just finished loading: freeze the captured duration and show a 100%
      // green bar for 600ms.
      loadDurationMs = lastElapsedMs;
      settled = true;

      const id = setTimeout(() => {
        settled = false;
        loadDurationMs = null;
      }, 600);

      return () => {
        clearTimeout(id);
      };
    }
  });

  let progress = $derived(computeLoadProgress(model, now));

  // Peer models have no local load timing (LoadInfo is only populated for local
  // models), so a peer would otherwise show a perpetual indeterminate bar —
  // gate it here so every call site is consistent.
  const showBar = $derived(!model.peerID && (!!progress || settled));
  const pct = $derived(settled ? 100 : (progress?.pct ?? null));
  // Bar-fill colour tells the same story as the sidebar dot: while estimating,
  // warm amber→green with progress (inline color-mix, since the hue is
  // per-frame and not a static class); flat amber once overrun so a near-green
  // fill can't imply "almost done"; solid green on the done-hold.
  const fillClass = $derived(
    settled
      ? "bg-success"
      : progress?.mode === "overrun"
        ? "bg-warning"
        : "",
  );
  const fillStyle = $derived(
    !settled && progress?.mode === "estimated" && pct !== null
      ? `background-color: ${loadFillColor(pct)}`
      : "",
  );
  const isPulsing = $derived(progress?.mode === "overrun");
  const label = $derived(
    settled
      ? `Loaded in ${formatDuration(loadDurationMs ?? 0, { precision: 1 })}`
      : progress
        ? formatLoadProgressLabel(progress)
        : "",
  );
  // Label colour tracks the fill so bar and text tell one story: green once
  // done, amber when the estimate has been overrun, plain foreground otherwise.
  const labelClass = $derived(
    settled
      ? "text-success font-medium"
      : progress?.mode === "overrun"
        ? "text-warning"
        : "text-foreground/80",
  );
  // While estimating, warm the text along the same amber→green ramp as the bar
  // but floored (loadLabelColor) so early amber stays legible as small text on a
  // light background; other modes keep their flat class colour (green done,
  // amber overrun, neutral indeterminate — no estimate to warm toward yet).
  const labelStyle = $derived(
    !settled && progress?.mode === "estimated" && pct !== null
      ? `color: ${loadLabelColor(pct)}`
      : "",
  );
</script>

{#if showBar}
  <div
    role="progressbar"
    aria-valuemin={0}
    aria-valuemax={100}
    aria-valuenow={pct !== null ? pct : undefined}
    aria-label={`Loading ${model.name || model.id}`}
    class="h-1.5 w-full overflow-hidden rounded-full bg-muted {className}"
  >
    {#if progress?.mode === "indeterminate"}
      <div
        class="h-full w-1/3 rounded-full bg-warning"
        style="animation: ls-slide 1.2s linear infinite;"
      ></div>
    {:else}
      <div
        class="h-full rounded-full {fillClass} {isPulsing ? 'animate-pulse' : ''} transition-[width] duration-300"
        style={`width: ${pct ?? 0}%${fillStyle ? `; ${fillStyle}` : ""}`}
      ></div>
    {/if}
  </div>
  <span class="text-xs tabular-nums {labelClass}" style={labelStyle}>{label}</span>
{/if}

<style>
  /* Referenced from an inline style attribute, which Svelte does not rewrite,
     so declare the keyframes global (the -global- prefix is stripped from the
     emitted name) to keep the inline `ls-slide` reference valid. */
  @keyframes -global-ls-slide {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(300%);
    }
  }
</style>
