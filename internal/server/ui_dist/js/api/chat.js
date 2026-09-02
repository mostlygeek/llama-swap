// Chat streaming client, ported verbatim (types stripped) from lib/chatApi.ts.
// Supports three wire formats — OpenAI chat-completions, Anthropic messages,
// and OpenAI responses — selected by the `endpoint` option. Each parser is a
// plain async generator over fetch().body.getReader() yielding
// { content, reasoning_content?, done } chunks.

function parseDataUrl(url) {
  const match = /^data:([^;]+);base64,(.*)$/i.exec(url);
  if (!match) {
    throw new Error("Image is not a base64 data URL");
  }
  return { media_type: match[1], data: match[2] };
}

function splitSystemMessages(messages) {
  const systemParts = [];
  const rest = [];
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

function buildChatCompletionsBody(model, messages, options) {
  // Tool results keep their name/tool_call_id alongside assistant tool_calls so
  // an agent conversation round-trips through the backend unchanged.
  const mapped = messages.map((m) => {
    const out = { role: m.role, content: m.content };
    if (m.role === "assistant" && m.tool_calls?.length) {
      out.tool_calls = m.tool_calls;
    }
    if (m.role === "tool") {
      out.tool_call_id = m.tool_call_id;
      if (m.name) out.name = m.name;
    }
    return out;
  });
  return {
    model,
    messages: mapped,
    stream: true,
    temperature: options?.temperature,
    ...(options?.max_tokens ? { max_tokens: options.max_tokens } : {}),
    // Tools are only supported on the chat-completions endpoint.
    ...(options?.tools?.length
      ? {
          tools: options.tools.map((t) => ({
            type: "function",
            function: {
              name: t.function.name,
              description: t.function.description,
              parameters: t.function.parameters,
            },
          })),
        }
      : {}),
  };
}

function buildMessagesBody(model, messages, options) {
  const { system, rest } = splitSystemMessages(messages);
  const mapped = rest.map((m) => {
    if (typeof m.content === "string") {
      return { role: m.role, content: m.content };
    }
    const blocks = [];
    for (const part of m.content) {
      if (part.type === "text") {
        blocks.push({ type: "text", text: part.text });
      } else if (m.role !== "assistant") {
        const { media_type, data } = parseDataUrl(part.image_url.url);
        blocks.push({ type: "image", source: { type: "base64", media_type, data } });
      }
    }
    return { role: m.role, content: blocks };
  });

  const body = {
    model,
    messages: mapped,
    stream: true,
    max_tokens: options?.max_tokens ?? 4096,
  };
  if (system) body.system = system;
  if (options?.temperature !== undefined) body.temperature = options.temperature;
  return body;
}

function buildResponsesBody(model, messages, options) {
  const { system, rest } = splitSystemMessages(messages);
  const input = rest.map((m) => {
    const isAssistant = m.role === "assistant";
    if (typeof m.content === "string") {
      const partType = isAssistant ? "output_text" : "input_text";
      return { role: m.role, content: [{ type: partType, text: m.content }] };
    }
    const content = m.content.map((part) => {
      if (part.type === "text") {
        return { type: isAssistant ? "output_text" : "input_text", text: part.text };
      }
      return { type: "input_image", image_url: part.image_url.url };
    });
    return { role: m.role, content };
  });

  const body = {
    model,
    input,
    stream: true,
  };
  if (system) body.instructions = system;
  if (options?.temperature !== undefined) body.temperature = options.temperature;
  if (options?.max_tokens) body.max_output_tokens = options.max_tokens;
  return body;
}

function buildRequest(endpoint, model, messages, options) {
  const url = "/" + endpoint;
  // Tools are a chat-completions-only feature; drop them rather than error.
  const opts = options?.tools?.length && endpoint !== "v1/chat/completions"
    ? { ...options, tools: undefined }
    : options;
  switch (endpoint) {
    case "v1/messages":
      return { url, body: buildMessagesBody(model, messages, opts) };
    case "v1/responses":
      return { url, body: buildResponsesBody(model, messages, opts) };
    case "v1/chat/completions":
    default:
      return { url, body: buildChatCompletionsBody(model, messages, opts) };
  }
}

export function parseChatCompletionsLine(line) {
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
    const tool_calls = Array.isArray(delta?.tool_calls) ? delta.tool_calls : undefined;
    const finish_reason = choice?.finish_reason || undefined;

    if (content || reasoning_content || tool_calls || finish_reason) {
      return { content, reasoning_content, tool_calls, finish_reason, done: false };
    }
    return null;
  } catch {
    return null;
  }
}

async function* parseChatCompletionsStream(reader) {
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

export function parseSSEEventBlock(block) {
  let event = "";
  const dataLines = [];
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

async function* parseMessagesStream(reader) {
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

async function* parseResponsesStream(reader) {
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

export function parseStream(endpoint, reader) {
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

export async function* streamChatCompletion(model, messages, signal, options) {
  const endpoint = options?.endpoint ?? "v1/chat/completions";
  const { url, body } = buildRequest(endpoint, model, messages, options);

  const response = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
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
