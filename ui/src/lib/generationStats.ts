import type { StreamChunk, StreamUsage } from "./chatApi";
import type { GenerationStats, PhaseStats } from "./types";

/**
 * Accumulates what a streamed turn reveals about its own cost: when the
 * request started, when tokens started and stopped arriving, how many chunks
 * came through in each phase, and whatever usage the backend reported.
 *
 * Chunks stand in for tokens until the backend says otherwise. llama.cpp
 * streams one token per chunk (a multibyte character can span several), so
 * the estimate is close and is replaced by the real count on the final chunk.
 */
export interface GenerationTracker {
  startedAt: number;
  firstTokenAt?: number;
  lastTokenAt?: number;
  /** When the first answer chunk arrived, which ends the thinking phase. */
  answerStartedAt?: number;
  reasoningChunks: number;
  answerChunks: number;
  usage: StreamUsage;
  finishReason?: string;
  /** The user stopped the turn, so no backend finish_reason will arrive. */
  cancelled?: boolean;
  /** Context window of the model the request went to, if known. */
  contextLength?: number;
}

export function startTracking(now: number, contextLength?: number): GenerationTracker {
  return { startedAt: now, reasoningChunks: 0, answerChunks: 0, usage: {}, contextLength };
}

/** Folds one streamed chunk into the tracker. */
export function trackChunk(tracker: GenerationTracker, chunk: StreamChunk, now: number): void {
  const isAnswer = Boolean(chunk.content) || Boolean(chunk.tool_calls?.length);
  const isReasoning = !isAnswer && Boolean(chunk.reasoning_content);
  if (isAnswer || isReasoning) {
    tracker.firstTokenAt ??= now;
    tracker.lastTokenAt = now;
    if (isAnswer) {
      tracker.answerChunks += 1;
      tracker.answerStartedAt ??= now;
    } else {
      tracker.reasoningChunks += 1;
    }
  }
  if (chunk.usage) {
    // Later reports win field by field: /v1/messages sends input tokens on
    // message_start and output tokens on message_delta.
    tracker.usage = { ...tracker.usage, ...chunk.usage };
  }
  if (chunk.finish_reason) tracker.finishReason = chunk.finish_reason;
}

/** Records that the user stopped the turn. */
export function markCancelled(tracker: GenerationTracker): void {
  tracker.cancelled = true;
}

function perSecond(tokens: number | undefined, ms: number | undefined): number | undefined {
  return tokensPerSecond(tokens, ms);
}

/**
 * The stats to display at time `now`. While the turn is still streaming the
 * clocks run to `now`: the prompt clock until the first token arrives, the
 * generation clock after it. Once the turn is done they stop at the last token.
 */
export function currentStats(tracker: GenerationTracker, now: number, streaming: boolean): GenerationStats {
  const { usage, startedAt, firstTokenAt, lastTokenAt, answerStartedAt, reasoningChunks, answerChunks } = tracker;
  const chunks = reasoningChunks + answerChunks;
  const end = streaming ? now : (lastTokenAt ?? now);

  const prompt: PhaseStats = { approxTokens: false, approxTimings: usage.prompt_ms === undefined };
  if (usage.prompt_tokens !== undefined) prompt.tokens = usage.prompt_tokens;
  if (usage.prompt_ms !== undefined) {
    prompt.ms = usage.prompt_ms;
  } else if (firstTokenAt !== undefined) {
    prompt.ms = firstTokenAt - startedAt;
  } else if (streaming) {
    prompt.ms = now - startedAt;
  }
  if (prompt.tokens !== undefined) {
    prompt.perSecond = perSecond(prompt.tokens - (usage.cached_tokens ?? 0), prompt.ms);
  }

  const generation: PhaseStats = {
    approxTokens: usage.completion_tokens === undefined,
    approxTimings: usage.completion_ms === undefined,
  };
  if (usage.completion_tokens !== undefined) {
    generation.tokens = usage.completion_tokens;
  } else if (chunks > 0) {
    generation.tokens = chunks;
  }
  if (usage.completion_ms !== undefined) {
    generation.ms = usage.completion_ms;
  } else if (firstTokenAt !== undefined) {
    generation.ms = end - firstTokenAt;
  }
  generation.perSecond = perSecond(generation.tokens, generation.ms);

  const stats: GenerationStats = { prompt, generation };
  if (usage.cached_tokens !== undefined) stats.cachedTokens = usage.cached_tokens;
  if (usage.draft_tokens !== undefined) stats.draftTokens = usage.draft_tokens;
  if (usage.draft_accepted !== undefined) stats.draftAccepted = usage.draft_accepted;
  if (firstTokenAt !== undefined) {
    stats.firstTokenMs = firstTokenAt - startedAt;
    stats.wallMs = end - startedAt;
  }
  // A cancelled turn gets no finish_reason of its own, so the client supplies
  // one; a backend that already reported why it stopped still wins.
  if (tracker.finishReason) {
    stats.finishReason = tracker.finishReason;
  } else if (tracker.cancelled) {
    stats.finishReason = "cancelled";
  }
  if (tracker.contextLength) stats.contextLength = tracker.contextLength;
  if (reasoningChunks === 0 || firstTokenAt === undefined) return stats;

  // Still thinking, or the turn ended without an answer: thinking is all of it.
  if (answerChunks === 0 || answerStartedAt === undefined) {
    stats.reasoning = { ...generation };
    return stats;
  }

  // The backend reports one total, so it is split by chunk ratio; the phase
  // boundary and both durations are wall clock.
  const total = generation.tokens ?? chunks;
  const reasoningTokens =
    usage.completion_tokens === undefined ? reasoningChunks : Math.round((total * reasoningChunks) / chunks);
  const reasoningMs = answerStartedAt - firstTokenAt;
  const answerMs = end - answerStartedAt;
  stats.reasoning = {
    tokens: reasoningTokens,
    ms: reasoningMs,
    perSecond: perSecond(reasoningTokens, reasoningMs),
    approxTokens: true,
    approxTimings: true,
  };
  stats.answer = {
    tokens: total - reasoningTokens,
    ms: answerMs,
    perSecond: perSecond(total - reasoningTokens, answerMs),
    approxTokens: true,
    approxTimings: true,
  };
  return stats;
}

/** Tokens per second, or undefined when either side is unknown or zero. */
export function tokensPerSecond(tokens?: number, ms?: number): number | undefined {
  if (tokens === undefined || ms === undefined || ms <= 0 || tokens <= 0) return undefined;
  return (tokens / ms) * 1000;
}
