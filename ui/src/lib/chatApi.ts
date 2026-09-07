import type { ChatMessage, ContentPart } from "./types";
import { playgroundSessionHeaders } from "./playgroundSession";

export type Endpoint = "v1/chat/completions" | "v1/messages" | "v1/responses";

/** One entry of the OpenAI-format `tools` array, mapped from an MCP tools/list. */
export interface ToolDefinition {
  type: "function";
  function: {
    name: string;
    description: string;
    parameters: Record<string, unknown>;
    /** Human-friendly display name from the MCP tool's `title`, when it has one. */
    title?: string;
  };
}

/**
 * A fragment of a tool call. Backends disagree on how these are split: some
 * omit `index`, some repeat the full `name` on every chunk. See
 * accumulateToolCalls in agentLoop.ts for how they are reassembled.
 */
export interface ToolCallDelta {
  index?: number;
  id?: string;
  type?: string;
  function?: { name?: string; arguments?: string };
}

/**
 * Token counts and timings reported by the backend, normalised across the
 * endpoints. Every field is optional: OpenAI-style `usage` gives only the
 * counts, llama.cpp's `timings` adds the time spent on each phase and the
 * speculative-decoding draft counts.
 */
export interface StreamUsage {
  /** The whole prompt, cached part included. */
  prompt_tokens?: number;
  /** Prompt tokens reused from the KV cache rather than processed. */
  cached_tokens?: number;
  completion_tokens?: number;
  /** Time spent processing the non-cached part of the prompt. */
  prompt_ms?: number;
  completion_ms?: number;
  /** Speculative decoding / MTP: tokens proposed by the draft, and accepted. */
  draft_tokens?: number;
  draft_accepted?: number;
}

export interface StreamChunk {
  content: string;
  reasoning_content?: string;
  tool_calls?: ToolCallDelta[];
  finish_reason?: string;
  /** Present on the chunk(s) that carry usage; typically the last one. */
  usage?: StreamUsage;
  done: boolean;
}

export interface ChatOptions {
  temperature?: number;
  endpoint?: Endpoint;
  max_tokens?: number;
  tools?: ToolDefinition[];
  tool_choice?: "auto" | "none" | "required";
  /**
   * Ask llama.cpp for `timings` on every streamed chunk instead of only the
   * last one. Defaults to on; see perTokenTimingsWanted.
   */
  timingsPerToken?: boolean;
}

function parseDataUrl(url: string): { media_type: string; data: string } {
  const match = /^data:([^;]+);base64,(.*)$/i.exec(url);
  if (!match) {
    throw new Error("Image is not a base64 data URL");
  }
  return { media_type: match[1], data: match[2] };
}

function splitSystemMessages(messages: ChatMessage[]): { system: string; rest: ChatMessage[] } {
  const systemParts: string[] = [];
  const rest: ChatMessage[] = [];
  for (const msg of messages) {
    // /v1/messages and /v1/responses are text-only here, but the history can
    // still hold tool messages from an earlier agent turn if the user switched
    // endpoints mid-conversation. Passing one through is a hard 400 upstream.
    if (msg.role === "tool") {
      continue;
    }
    if (msg.role === "system") {
      if (typeof msg.content === "string") {
        systemParts.push(msg.content);
      } else {
        for (const part of msg.content) {
          if (part.type === "text") systemParts.push(part.text);
        }
      }
    } else {
      rest.push(msg);
    }
  }
  return { system: systemParts.join("\n\n"), rest };
}

function buildChatCompletionsBody(model: string, messages: ChatMessage[], options?: ChatOptions): object {
  return {
    model,
    messages: messages.map((m) => {
      // content is coerced to "" rather than left undefined: an assistant turn
      // that only made tool calls has no text, and several backends reject a
      // message with a missing content field.
      const out: Record<string, unknown> = { role: m.role, content: m.content ?? "" };
      if (m.role === "assistant" && m.tool_calls?.length) {
        out.tool_calls = m.tool_calls;
      }
      if (m.role === "tool") {
        out.tool_call_id = m.tool_call_id;
        if (m.name) out.name = m.name;
      }
      return out;
    }),
    stream: true,
    // Asks for a final chunk carrying token usage so the playground can show
    // per-turn stats. Standard OpenAI; backends that predate it ignore it.
    stream_options: { include_usage: true },
    // llama.cpp extension: repeat the `timings` block on every chunk. Without
    // it the exact numbers only arrive in the final chunk, which a cancelled
    // stream never delivers.
    ...(options?.timingsPerToken === false ? {} : { timings_per_token: true }),
    temperature: options?.temperature,
    ...(options?.max_tokens ? { max_tokens: options.max_tokens } : {}),
    ...(options?.tools?.length
      ? {
          // Strip UI-only metadata (e.g. `function.title`) so only the wire
          // fields the API defines reach the backend.
          tools: options.tools.map((t) => ({
            type: t.type,
            function: {
              name: t.function.name,
              description: t.function.description,
              parameters: t.function.parameters,
            },
          })),
          tool_choice: options.tool_choice ?? "auto",
        }
      : {}),
  };
}

function buildMessagesBody(model: string, messages: ChatMessage[], options?: ChatOptions): object {
  const { system, rest } = splitSystemMessages(messages);
  const mapped = rest.map((m) => {
    if (typeof m.content === "string") {
      return { role: m.role, content: m.content };
    }
    const blocks: object[] = [];
    for (const part of m.content as ContentPart[]) {
      if (part.type === "text") {
        blocks.push({ type: "text", text: part.text });
      } else if (m.role !== "assistant") {
        const { media_type, data } = parseDataUrl(part.image_url.url);
        blocks.push({ type: "image", source: { type: "base64", media_type, data } });
      }
    }
    return { role: m.role, content: blocks };
  });

  const body: Record<string, unknown> = {
    model,
    messages: mapped,
    stream: true,
    max_tokens: options?.max_tokens ?? 4096,
  };
  if (system) body.system = system;
  if (options?.temperature !== undefined) body.temperature = options.temperature;
  return body;
}

function buildResponsesBody(model: string, messages: ChatMessage[], options?: ChatOptions): object {
  const { system, rest } = splitSystemMessages(messages);
  const input = rest.map((m) => {
    const isAssistant = m.role === "assistant";
    if (typeof m.content === "string") {
      const partType = isAssistant ? "output_text" : "input_text";
      return { role: m.role, content: [{ type: partType, text: m.content }] };
    }
    const content = m.content.map((part: ContentPart) => {
      if (part.type === "text") {
        return { type: isAssistant ? "output_text" : "input_text", text: part.text };
      }
      return { type: "input_image", image_url: part.image_url.url };
    });
    return { role: m.role, content };
  });

  const body: Record<string, unknown> = {
    model,
    input,
    stream: true,
  };
  if (system) body.instructions = system;
  if (options?.temperature !== undefined) body.temperature = options.temperature;
  if (options?.max_tokens) body.max_output_tokens = options.max_tokens;
  return body;
}

// Exported for tests: this is the whole request-shaping surface.
export function buildRequest(
  endpoint: Endpoint,
  model: string,
  messages: ChatMessage[],
  options?: ChatOptions
): { url: string; body: object } {
  const url = "/" + endpoint;
  if (options?.tools?.length && endpoint !== "v1/chat/completions") {
    throw new Error("Tool calling is only supported on /v1/chat/completions");
  }
  switch (endpoint) {
    case "v1/messages":
      return { url, body: buildMessagesBody(model, messages, options) };
    case "v1/responses":
      return { url, body: buildResponsesBody(model, messages, options) };
    case "v1/chat/completions":
    default:
      return { url, body: buildChatCompletionsBody(model, messages, options) };
  }
}

function asNumber(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

/**
 * Reads a usage object in either the OpenAI (`prompt_tokens`) or Anthropic
 * (`input_tokens`) spelling, with the cached-token count from whichever of
 * the three places the endpoints put it. Returns undefined when neither
 * token count is present.
 */
function parseUsageObject(usage: unknown): StreamUsage | undefined {
  if (!usage || typeof usage !== "object") return undefined;
  const u = usage as Record<string, unknown>;
  const out: StreamUsage = {};
  const prompt = asNumber(u.prompt_tokens) ?? asNumber(u.input_tokens);
  const completion = asNumber(u.completion_tokens) ?? asNumber(u.output_tokens);
  if (prompt !== undefined) out.prompt_tokens = prompt;
  if (completion !== undefined) out.completion_tokens = completion;
  if (prompt === undefined && completion === undefined) return undefined;
  const cached =
    asNumber((u.prompt_tokens_details as Record<string, unknown> | undefined)?.cached_tokens) ??
    asNumber((u.input_tokens_details as Record<string, unknown> | undefined)?.cached_tokens) ??
    asNumber(u.cache_read_input_tokens);
  if (cached !== undefined) out.cached_tokens = cached;
  return out;
}

/**
 * Reads llama.cpp's `timings` object. Its `prompt_n` counts only the tokens
 * actually processed, so the whole prompt is that plus `cache_n`.
 */
function parseTimingsObject(timings: unknown): StreamUsage | undefined {
  if (!timings || typeof timings !== "object") return undefined;
  const t = timings as Record<string, unknown>;
  const out: StreamUsage = {};
  const promptN = asNumber(t.prompt_n);
  const cacheN = asNumber(t.cache_n);
  const predictedN = asNumber(t.predicted_n);
  const promptMs = asNumber(t.prompt_ms);
  const predictedMs = asNumber(t.predicted_ms);
  const draftN = asNumber(t.draft_n);
  const draftAccepted = asNumber(t.draft_n_accepted);
  if (promptN !== undefined) out.prompt_tokens = promptN + (cacheN ?? 0);
  if (cacheN !== undefined) out.cached_tokens = cacheN;
  if (predictedN !== undefined) out.completion_tokens = predictedN;
  if (promptMs !== undefined) out.prompt_ms = promptMs;
  if (predictedMs !== undefined) out.completion_ms = predictedMs;
  if (draftN !== undefined) out.draft_tokens = draftN;
  if (draftAccepted !== undefined) out.draft_accepted = draftAccepted;
  return Object.keys(out).length ? out : undefined;
}

/**
 * Maps an Anthropic `stop_reason` onto the OpenAI finish_reason vocabulary
 * the stats use, so every endpoint reports why a turn ended the same way.
 */
export function normalizeAnthropicStopReason(reason: unknown): string | undefined {
  if (typeof reason !== "string" || !reason) return undefined;
  switch (reason) {
    case "end_turn":
    case "stop_sequence":
      return "stop";
    case "max_tokens":
      return "length";
    case "tool_use":
      return "tool_calls";
    default:
      return reason;
  }
}

/**
 * Maps a Responses API terminal event (`response.completed`, `.incomplete`,
 * `.failed`) and the response object it carries onto a finish_reason.
 */
export function normalizeResponsesStop(event: string, response: unknown): string | undefined {
  const r = (response && typeof response === "object" ? response : {}) as Record<string, unknown>;
  const details = r.incomplete_details as Record<string, unknown> | undefined;
  if (event === "response.incomplete" || r.status === "incomplete") {
    if (details?.reason === "max_output_tokens") return "length";
    return typeof details?.reason === "string" ? details.reason : "incomplete";
  }
  if (event === "response.failed" || r.status === "failed") return "error";
  if (event === "response.completed") return "stop";
  return undefined;
}

// Exported for tests.
export function parseChatCompletionsLine(line: string): StreamChunk | null {
  const trimmed = line.trim();
  if (!trimmed || !trimmed.startsWith("data: ")) {
    return null;
  }

  const data = trimmed.slice(6);
  if (data === "[DONE]") {
    return { content: "", done: true };
  }

  try {
    const parsed = JSON.parse(data);
    const choice = parsed.choices?.[0];
    const delta = choice?.delta;
    const content = delta?.content || "";
    const reasoning_content = delta?.reasoning_content || delta?.reasoning || "";
    const tool_calls = Array.isArray(delta?.tool_calls) ? (delta.tool_calls as ToolCallDelta[]) : undefined;
    const finish_reason = choice?.finish_reason || undefined;
    const usageFields = parseUsageObject(parsed.usage);
    const timingFields = parseTimingsObject(parsed.timings);
    // usage wins for the token counts (its prompt_tokens is always the whole
    // prompt); timings supply everything else.
    const usage = usageFields || timingFields ? { ...timingFields, ...usageFields } : undefined;

    if (content || reasoning_content || tool_calls || finish_reason || usage) {
      return { content, reasoning_content, tool_calls, finish_reason, usage, done: false };
    }
    return null;
  } catch {
    return null;
  }
}

async function* parseChatCompletionsStream(
  reader: ReadableStreamDefaultReader<Uint8Array>
): AsyncGenerator<StreamChunk> {
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() || "";

    for (const line of lines) {
      const result = parseChatCompletionsLine(line);
      if (result?.done) {
        yield result;
        return;
      }
      if (result) {
        yield result;
      }
    }
  }

  const result = parseChatCompletionsLine(buffer);
  if (result && !result.done) {
    yield result;
  }
}

function parseSSEEventBlock(block: string): { event: string; data: string } | null {
  let event = "";
  const dataLines: string[] = [];
  for (const rawLine of block.split("\n")) {
    const line = rawLine.replace(/\r$/, "");
    if (!line || line.startsWith(":")) continue;
    if (line.startsWith("event:")) {
      event = line.slice(6).trim();
    } else if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trim());
    }
  }
  if (dataLines.length === 0 && !event) return null;
  return { event, data: dataLines.join("\n") };
}

async function* parseMessagesStream(
  reader: ReadableStreamDefaultReader<Uint8Array>
): AsyncGenerator<StreamChunk> {
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const blocks = buffer.split("\n\n");
    buffer = blocks.pop() || "";

    for (const block of blocks) {
      const parsed = parseSSEEventBlock(block);
      if (!parsed) continue;
      if (parsed.event === "message_stop") {
        yield { content: "", done: true };
        return;
      }
      if (!parsed.data) continue;
      try {
        const json = JSON.parse(parsed.data);
        // Input tokens arrive on message_start; output tokens and the stop
        // reason on message_delta.
        if (parsed.event === "message_start" || parsed.event === "message_delta") {
          const usage = parseUsageObject(parsed.event === "message_start" ? json.message?.usage : json.usage);
          const finish_reason =
            parsed.event === "message_delta" ? normalizeAnthropicStopReason(json.delta?.stop_reason) : undefined;
          if (usage || finish_reason) yield { content: "", usage, finish_reason, done: false };
          continue;
        }
        if (parsed.event !== "content_block_delta") continue;
        const delta = json.delta;
        if (!delta) continue;
        if (delta.type === "text_delta" && delta.text) {
          yield { content: delta.text, done: false };
        } else if (delta.type === "thinking_delta" && delta.thinking) {
          yield { content: "", reasoning_content: delta.thinking, done: false };
        }
      } catch {
        // ignore malformed event
      }
    }
  }
}

async function* parseResponsesStream(
  reader: ReadableStreamDefaultReader<Uint8Array>
): AsyncGenerator<StreamChunk> {
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const blocks = buffer.split("\n\n");
    buffer = blocks.pop() || "";

    for (const block of blocks) {
      const parsed = parseSSEEventBlock(block);
      if (!parsed) continue;
      if (!parsed.data) continue;
      try {
        const json = JSON.parse(parsed.data);
        if (
          parsed.event === "response.completed" ||
          parsed.event === "response.incomplete" ||
          parsed.event === "response.failed"
        ) {
          const usage = parseUsageObject(json.response?.usage);
          const finish_reason = normalizeResponsesStop(parsed.event, json.response);
          if (usage || finish_reason) yield { content: "", usage, finish_reason, done: false };
          yield { content: "", done: true };
          return;
        }
        if (parsed.event === "response.output_text.delta" && json.delta) {
          yield { content: json.delta, done: false };
        } else if (parsed.event === "response.reasoning_summary_text.delta" && json.delta) {
          yield { content: "", reasoning_content: json.delta, done: false };
        }
      } catch {
        // ignore malformed event
      }
    }
  }
}

function parseStream(
  endpoint: Endpoint,
  reader: ReadableStreamDefaultReader<Uint8Array>
): AsyncGenerator<StreamChunk> {
  switch (endpoint) {
    case "v1/messages":
      return parseMessagesStream(reader);
    case "v1/responses":
      return parseResponsesStream(reader);
    case "v1/chat/completions":
    default:
      return parseChatCompletionsStream(reader);
  }
}

/**
 * Whether to keep asking for per-chunk timings. The parameter is a llama.cpp
 * extension, so a backend that validates its request body strictly answers
 * 400 and names the field. The first such rejection turns it off for the
 * rest of the session and the request is retried without it. A 400 that does
 * not mention the field is an ordinary error and is reported as one.
 */
let perTokenTimingsWanted = true;
const PER_TOKEN_TIMINGS_FIELD = "timings_per_token";

export async function* streamChatCompletion(
  model: string,
  messages: ChatMessage[],
  signal?: AbortSignal,
  options?: ChatOptions
): AsyncGenerator<StreamChunk> {
  const endpoint = options?.endpoint ?? "v1/chat/completions";
  const send = (timingsPerToken: boolean) => {
    const { url, body } = buildRequest(endpoint, model, messages, { ...options, timingsPerToken });
    return fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...playgroundSessionHeaders,
      },
      body: JSON.stringify(body),
      signal,
    });
  };

  const askedForTimings = perTokenTimingsWanted && endpoint === "v1/chat/completions";
  let response = await send(askedForTimings);

  if (!response.ok) {
    const errorText = await response.text();
    const rejectedExtension = response.status === 400 && askedForTimings && errorText.includes(PER_TOKEN_TIMINGS_FIELD);
    if (!rejectedExtension) {
      throw new Error(`Chat API error: ${response.status} - ${errorText}`);
    }
    perTokenTimingsWanted = false;
    response = await send(false);
    if (!response.ok) {
      throw new Error(`Chat API error: ${response.status} - ${await response.text()}`);
    }
  }

  const reader = response.body?.getReader();
  if (!reader) {
    throw new Error("Response body is not readable");
  }

  try {
    for await (const chunk of parseStream(endpoint, reader)) {
      yield chunk;
      if (chunk.done) return;
    }
    yield { content: "", done: true };
  } finally {
    reader.releaseLock();
  }
}
