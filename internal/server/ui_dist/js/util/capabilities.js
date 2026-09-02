// Capability badge helpers. Ported from lib/capabilities.ts. Shared by the
// model Details tab and the Models list so labels live in exactly one place.

export const capabilityLabels = {
  vision: "Vision",
  audio_transcriptions: "Transcription",
  audio_speech: "Speech",
  image_generation: "Image Gen",
  image_to_image: "Img→Img",
  function_calling: "Function Calling",
  reranker: "Reranker",
};

// Muted CSS classes per capability badge key, so each is visually distinct on
// the Models list. Var(--...) pairs keep contrast in light and dark themes.
export const capabilityBadgeClass = {
  vision: "cap-vision",
  audio_transcriptions: "cap-transcription",
  audio_speech: "cap-speech",
  image_generation: "cap-imagegen",
  image_to_image: "cap-img2img",
  function_calling: "cap-tools",
  reranker: "cap-reranker",
  context: "cap-context",
};

/** Formats a token count as a compact context-window badge (128000 -> "128K"). */
export function formatContextLength(tokens) {
  if (tokens <= 0) return "";
  if (tokens >= 1_000_000) {
    const m = tokens / 1_000_000;
    return Number.isInteger(m) ? `${m}M` : `${m.toFixed(1)}M`;
  }
  if (tokens >= 1_000) {
    const k = tokens / 1_000;
    return Number.isInteger(k) ? `${k}K` : `${Math.round(k)}K`;
  }
  return String(tokens);
}

/**
 * Returns the capability badges for a Models list row: every reported
 * capability in canonical order (mirroring the Details tab tag set), with the
 * context window last so it sits on the right of the group.
 */
export function listCapabilityBadges(model) {
  const badges = [];

  const caps = model.capabilities ?? {};
  for (const key of Object.keys(capabilityLabels)) {
    if (caps[key]) {
      badges.push({ key, label: capabilityLabels[key] });
    }
  }

  const ctx = model.context_length ?? 0;
  if (ctx > 0) {
    badges.push({ key: "context", label: formatContextLength(ctx) });
  }

  return badges;
}
