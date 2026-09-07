import { describe, expect, it } from "vitest";
import { currentStats, markCancelled, startTracking, tokensPerSecond, trackChunk } from "./generationStats";

const text = (content: string) => ({ content, done: false });
const think = (reasoning_content: string) => ({ content: "", reasoning_content, done: false });
const approx = { approxTokens: true, approxTimings: true };

describe("generationStats", () => {
  it("estimates from chunks and wall clock while streaming", () => {
    const t = startTracking(1000);
    trackChunk(t, text("a"), 1500);
    trackChunk(t, text("b"), 1600);
    trackChunk(t, text("c"), 1700);

    expect(currentStats(t, 2000, true)).toEqual({
      prompt: { ms: 500, approxTokens: false, approxTimings: true },
      generation: { tokens: 3, ms: 500, perSecond: 6, ...approx }, // runs to `now` while streaming
      firstTokenMs: 500,
      wallMs: 1000,
    });
    expect(currentStats(t, 2000, false).generation.ms).toBe(200); // stops at last token
  });

  it("counts tool-call chunks as answer tokens", () => {
    const t = startTracking(0);
    trackChunk(t, { content: "", tool_calls: [{ index: 0 }], done: false }, 20);
    expect(currentStats(t, 30, false).generation.tokens).toBe(1);
    expect(t.answerChunks).toBe(1);
  });

  it("ignores chunks that carry nothing but a finish_reason or usage", () => {
    const t = startTracking(0);
    trackChunk(t, { content: "", finish_reason: "stop", done: false }, 10);
    trackChunk(t, { content: "", usage: { completion_tokens: 0 }, done: false }, 20);
    expect(t.reasoningChunks + t.answerChunks).toBe(0);
    expect(t.firstTokenAt).toBeUndefined();
  });

  it("runs only the prompt clock before the first token", () => {
    const t = startTracking(0);
    expect(currentStats(t, 100, true)).toEqual({
      prompt: { ms: 100, approxTokens: false, approxTimings: true },
      generation: approx,
    });
    // a turn that ended without a single token has nothing to report
    expect(currentStats(t, 100, false)).toEqual({
      prompt: { approxTokens: false, approxTimings: true },
      generation: approx,
    });
  });

  it("prefers backend usage counts over chunk counts", () => {
    const t = startTracking(0);
    trackChunk(t, text("a"), 100);
    trackChunk(t, text("b"), 200);
    trackChunk(t, { content: "", usage: { prompt_tokens: 40, completion_tokens: 7 }, done: false }, 300);

    const { prompt, generation } = currentStats(t, 300, false);
    expect(prompt).toEqual({ tokens: 40, ms: 100, perSecond: 400, approxTokens: false, approxTimings: true });
    expect(generation).toEqual({ tokens: 7, ms: 100, perSecond: 70, approxTokens: false, approxTimings: true });
  });

  it("prefers backend timings over wall clock", () => {
    const t = startTracking(0);
    trackChunk(t, text("a"), 100);
    trackChunk(
      t,
      { content: "", usage: { prompt_tokens: 10, prompt_ms: 50, completion_tokens: 1, completion_ms: 20 }, done: false },
      300
    );

    expect(currentStats(t, 300, false)).toEqual({
      prompt: { tokens: 10, ms: 50, perSecond: 200, approxTokens: false, approxTimings: false },
      generation: { tokens: 1, ms: 20, perSecond: 50, approxTokens: false, approxTimings: false },
      firstTokenMs: 100,
      wallMs: 100,
    });
  });

  it("measures prompt throughput over the non-cached tokens only", () => {
    const t = startTracking(0);
    trackChunk(t, text("a"), 100);
    trackChunk(t, { content: "", usage: { prompt_tokens: 100, cached_tokens: 80, prompt_ms: 40 }, done: false }, 200);

    const stats = currentStats(t, 200, false);
    expect(stats.prompt.tokens).toBe(100);
    expect(stats.cachedTokens).toBe(80);
    expect(stats.prompt.perSecond).toBe(500); // 20 new tokens in 40ms
  });

  it("passes through draft counts and the finish reason", () => {
    const t = startTracking(0);
    trackChunk(t, text("a"), 100);
    trackChunk(t, { content: "", finish_reason: "length", done: false }, 150);
    trackChunk(t, { content: "", usage: { draft_tokens: 40, draft_accepted: 30 }, done: false }, 200);

    const stats = currentStats(t, 200, false);
    expect(stats.draftTokens).toBe(40);
    expect(stats.draftAccepted).toBe(30);
    expect(stats.finishReason).toBe("length");
  });

  it("merges usage that arrives in pieces", () => {
    const t = startTracking(0);
    trackChunk(t, { content: "", usage: { prompt_tokens: 12 }, done: false }, 1);
    trackChunk(t, { content: "", usage: { completion_tokens: 3 }, done: false }, 2);
    expect(t.usage).toEqual({ prompt_tokens: 12, completion_tokens: 3 });
  });

  describe("thinking", () => {
    it("reports thinking as the whole generation while the model is still thinking", () => {
      const t = startTracking(0);
      trackChunk(t, think("hm"), 100);
      trackChunk(t, think("hm"), 200);

      const stats = currentStats(t, 300, true);
      expect(stats.generation).toEqual({ tokens: 2, ms: 200, perSecond: 10, ...approx });
      expect(stats.reasoning).toEqual(stats.generation);
      expect(stats.answer).toBeUndefined();
    });

    it("splits thinking and answer at the first answer token", () => {
      const t = startTracking(0);
      trackChunk(t, think("a"), 100);
      trackChunk(t, think("b"), 200);
      trackChunk(t, think("c"), 300);
      trackChunk(t, text("x"), 400);
      trackChunk(t, text("y"), 500);

      const stats = currentStats(t, 500, false);
      expect(stats.generation).toEqual({ tokens: 5, ms: 400, perSecond: 12.5, ...approx });
      expect(stats.reasoning).toEqual({ tokens: 3, ms: 300, perSecond: 10, ...approx });
      expect(stats.answer).toEqual({ tokens: 2, ms: 100, perSecond: 20, ...approx });
    });

    it("splits a backend total by chunk ratio", () => {
      const t = startTracking(0);
      trackChunk(t, think("a"), 100);
      trackChunk(t, think("b"), 200);
      trackChunk(t, text("x"), 300);
      trackChunk(t, text("y"), 400);
      // 4 chunks, but the backend counted 6 tokens
      trackChunk(t, { content: "", usage: { completion_tokens: 6, completion_ms: 290 }, done: false }, 400);

      const stats = currentStats(t, 400, false);
      expect(stats.generation).toEqual({
        tokens: 6,
        ms: 290,
        perSecond: expect.closeTo(20.69, 2),
        approxTokens: false,
        approxTimings: false,
      });
      expect(stats.reasoning).toEqual({ tokens: 3, ms: 200, perSecond: 15, ...approx });
      expect(stats.answer).toEqual({ tokens: 3, ms: 100, perSecond: 30, ...approx });
    });

    it("keeps thinking as the whole turn when no answer ever came", () => {
      const t = startTracking(0);
      trackChunk(t, think("a"), 100);
      trackChunk(t, { content: "", usage: { completion_tokens: 1, completion_ms: 90 }, done: false }, 200);

      const stats = currentStats(t, 200, false);
      expect(stats.reasoning).toEqual({
        tokens: 1,
        ms: 90,
        perSecond: expect.closeTo(11.11, 2),
        approxTokens: false,
        approxTimings: false,
      });
      expect(stats.answer).toBeUndefined();
    });

    it("has no thinking phases for a plain answer", () => {
      const t = startTracking(0);
      trackChunk(t, text("x"), 100);
      const stats = currentStats(t, 100, false);
      expect(stats.reasoning).toBeUndefined();
      expect(stats.answer).toBeUndefined();
    });
  });

  describe("cancelling", () => {
    it("reports a cancelled turn as its own stop reason", () => {
      const t = startTracking(0);
      trackChunk(t, text("a"), 100);
      markCancelled(t);
      expect(currentStats(t, 200, false).finishReason).toBe("cancelled");
    });

    it("keeps the backend's reason when it already reported one", () => {
      const t = startTracking(0);
      trackChunk(t, { content: "", finish_reason: "stop", done: false }, 100);
      markCancelled(t);
      expect(currentStats(t, 200, false).finishReason).toBe("stop");
    });

    // Per-chunk timings mean an aborted turn still has exact backend numbers.
    it("keeps the numbers the backend reported before the abort", () => {
      const t = startTracking(0);
      trackChunk(t, { content: "a", usage: { prompt_tokens: 17, cached_tokens: 13, prompt_ms: 20 }, done: false }, 100);
      trackChunk(t, { content: "b", usage: { completion_tokens: 2, completion_ms: 60 }, done: false }, 160);
      markCancelled(t);

      const stats = currentStats(t, 200, false);
      expect(stats.prompt).toEqual({ tokens: 17, ms: 20, perSecond: 200, approxTokens: false, approxTimings: false });
      expect(stats.generation).toEqual({
        tokens: 2,
        ms: 60,
        perSecond: expect.closeTo(33.33, 2),
        approxTokens: false,
        approxTimings: false,
      });
      expect(stats.cachedTokens).toBe(13);
      expect(stats.finishReason).toBe("cancelled");
    });
  });

  // The dropdown can change after a turn; the turn keeps the window it ran in.
  it("carries the context window the turn was started with", () => {
    const t = startTracking(0, 32768);
    trackChunk(t, text("a"), 100);
    expect(currentStats(t, 100, false).contextLength).toBe(32768);
    expect(currentStats(startTracking(0), 100, false).contextLength).toBeUndefined();
  });

  it("computes tokens per second only when it is meaningful", () => {
    expect(tokensPerSecond(50, 2000)).toBe(25);
    expect(tokensPerSecond(undefined, 2000)).toBeUndefined();
    expect(tokensPerSecond(50, undefined)).toBeUndefined();
    expect(tokensPerSecond(50, 0)).toBeUndefined();
    expect(tokensPerSecond(0, 100)).toBeUndefined();
  });
});
