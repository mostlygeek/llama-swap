import type { PhaseStats } from "./types";

/** The phases a stats line can describe. */
export type PhaseKind = "prompt" | "reasoning" | "response" | "generation";

export type PhaseMetric = "tokens" | "time" | "speed" | "perToken";

export const PHASE_LABEL: Record<PhaseKind, string> = {
  prompt: "Prompt",
  reasoning: "Reasoning",
  response: "Response",
  generation: "Generation",
};

/** One-line description of a phase, for its row label. */
export const PHASE_TOOLTIP: Record<PhaseKind, string> = {
  prompt: "Prompt processing: reading everything sent to the model for this turn before it can generate.",
  reasoning: "Reasoning: what the model produced before starting its response.",
  response: "Response: the visible reply, after any reasoning.",
  generation: "Generation: everything the model produced for this turn, reasoning and response together.",
};

const SPLIT_NOTE =
  " ~ Estimated: the backend reports one total for the whole generation; it is split between reasoning and response by streamed chunks, and the boundary is the first response token.";
const CHUNK_NOTE = " ~ Estimated from streamed chunks until the backend reports the count.";
const BROWSER_NOTE = " ~ Measured in the browser, not by the backend.";

/** Tooltip for one value of a phase, explaining what it measures and how. */
export function phaseValueTooltip(kind: PhaseKind, metric: PhaseMetric, phase: PhaseStats): string {
  const approxSpeed = phase.approxTokens || phase.approxTimings;
  switch (metric) {
    case "tokens":
      switch (kind) {
        case "prompt":
          return "Prompt tokens: everything sent to the model for this turn (system prompt, history and message), including tokens reused from the cache.";
        case "reasoning":
          return "Reasoning tokens: produced before the response." + (phase.approxTokens ? SPLIT_NOTE : "");
        case "response":
          return "Response tokens: the visible reply." + (phase.approxTokens ? SPLIT_NOTE : "");
        case "generation":
          return "Generated tokens: reasoning and response together." + (phase.approxTokens ? CHUNK_NOTE : "");
      }
      break;
    case "time":
      switch (kind) {
        case "prompt":
          return phase.approxTimings
            ? "Time to first token, measured in the browser: covers queueing, model loading and prompt processing."
            : "Prefill time: how long the backend took to process the prompt tokens that were not already cached.";
        case "reasoning":
          return "Reasoning time: from the first reasoning token to the first response token." + (phase.approxTimings ? BROWSER_NOTE : "");
        case "response":
          return "Response time: from the first response token to the last one." + (phase.approxTimings ? BROWSER_NOTE : "");
        case "generation":
          return phase.approxTimings
            ? "Generation time: from the first to the last streamed token, measured in the browser."
            : "Generation time: how long the backend spent producing tokens, reasoning and response together.";
      }
      break;
    case "speed":
      switch (kind) {
        case "prompt":
          return (
            "Prefill speed: prompt tokens processed per second, counting only the tokens not served from the cache." +
            (phase.approxTimings ? " ~ Based on the browser-measured time to first token." : "")
          );
        case "reasoning":
          return "Reasoning speed: reasoning tokens generated per second." + (approxSpeed ? " ~ Derived from estimated values." : "");
        case "response":
          return "Response speed: reply tokens generated per second." + (approxSpeed ? " ~ Derived from estimated values." : "");
        case "generation":
          return "Generation speed: tokens generated per second over the whole turn." + (approxSpeed ? " ~ Derived from estimated values." : "");
      }
      break;
    case "perToken":
      return kind === "prompt"
        ? "Average time to process one prompt token that was not served from the cache."
        : "Average time to generate one token: the inverse of the speed.";
  }
  return "";
}

/** Tooltips for the details under the stats table. */
export const DETAIL_TOOLTIP = {
  cached:
    "Prompt tokens served from the backend's KV cache, so they cost no prefill time. The share of the whole prompt is in brackets.",
  draft:
    "Speculative decoding / MTP: tokens proposed by the draft, how many the main model accepted, and the acceptance rate. Higher means the draft is paying off.",
  firstToken:
    "Time from sending the request to the first streamed token, measured in the browser. Includes queueing, model loading and prompt processing.",
  wall: "Time from sending the request to the last streamed token, measured in the browser.",
  context:
    "How much of the model's context window this turn occupied: prompt plus generated tokens, against the model's context length.",
  stop: "Why generation stopped: the model reached the end of its turn, hit the max_tokens limit, called a tool, or you cancelled it.",
  waiting: "Waiting for the first token: the request has been sent and the model has not produced anything yet.",
  columnTokens: "Token counts. A ~ marks an estimate; hover a value for how it was obtained.",
  columnTime: "Time spent in each phase.",
  columnSpeed: "Tokens per second in each phase.",
  columnPerToken: "Average time per token in each phase.",
} as const;
