import type { ChatMessage, ContentPart } from "./types";
import { playgroundSessionHeaders } from "./playgroundSession";

export type Endpoint = "v1/chat/completions" | "v1/messages" | "v1/responses";

export interface StreamChunk {
  content: string;
  reasoning_content?: string;
  done: boolean;
}

export interface ChatOptions {
  temperature?: number;
  endpoint?: Endpoint;
  max_tokens?: number;
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
    messages: messages.map((m) => ({
      role: m.role,
      content: m.content,
    })),
    stream: true,
    temperature: options?.temperature,
    ...(options?.max_tokens ? { max_tokens: options.max_tokens } : {}),
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

function buildRequest(
  endpoint: Endpoint,
  model: string,
  messages: ChatMessage[],
  options?: ChatOptions
): { url: string; body: object } {
  const url = "/" + endpoint;
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

function parseChatCompletionsLine(line: string): StreamChunk | null {
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
    const delta = parsed.choices?.[0]?.delta;
    const content = delta?.content || "";
    const reasoning_content = delta?.reasoning_content || delta?.reasoning || "";

    if (content || reasoning_content) {
      return { content, reasoning_content, done: false };
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
      if (parsed.event !== "content_block_delta" || !parsed.data) continue;
      try {
        const json = JSON.parse(parsed.data);
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
      if (parsed.event === "response.completed") {
        yield { content: "", done: true };
        return;
      }
      if (!parsed.data) continue;
      try {
        const json = JSON.parse(parsed.data);
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

export async function* streamChatCompletion(
  model: string,
  messages: ChatMessage[],
  signal?: AbortSignal,
  options?: ChatOptions
): AsyncGenerator<StreamChunk> {
  const endpoint = options?.endpoint ?? "v1/chat/completions";
  const { url, body } = buildRequest(endpoint, model, messages, options);

  const response = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...playgroundSessionHeaders,
    },
    body: JSON.stringify(body),
    signal,
  });

  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Chat API error: ${response.status} - ${errorText}`);
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
