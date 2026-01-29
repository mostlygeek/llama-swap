import type { ChatMessage, ChatCompletionRequest } from "./types";

export interface StreamChunk {
  content: string;
  done: boolean;
}

export interface ChatOptions {
  temperature?: number;
}

export async function* streamChatCompletion(
  model: string,
  messages: ChatMessage[],
  signal?: AbortSignal,
  options?: ChatOptions
): AsyncGenerator<StreamChunk> {
  const request: ChatCompletionRequest = {
    model,
    messages,
    stream: true,
    temperature: options?.temperature,
  };

  const response = await fetch("/v1/chat/completions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(request),
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

  const decoder = new TextDecoder();
  let buffer = "";

  try {
    while (true) {
      const { done, value } = await reader.read();

      if (done) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";

      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed || !trimmed.startsWith("data: ")) {
          continue;
        }

        const data = trimmed.slice(6);

        if (data === "[DONE]") {
          yield { content: "", done: true };
          return;
        }

        try {
          const parsed = JSON.parse(data);
          const content = parsed.choices?.[0]?.delta?.content || "";
          if (content) {
            yield { content, done: false };
          }
        } catch {
          // Skip malformed JSON lines
        }
      }
    }

    // Process any remaining buffer
    if (buffer.trim()) {
      const trimmed = buffer.trim();
      if (trimmed.startsWith("data: ")) {
        const data = trimmed.slice(6);
        if (data !== "[DONE]") {
          try {
            const parsed = JSON.parse(data);
            const content = parsed.choices?.[0]?.delta?.content || "";
            if (content) {
              yield { content, done: false };
            }
          } catch {
            // Skip malformed JSON
          }
        }
      }
    }

    yield { content: "", done: true };
  } finally {
    reader.releaseLock();
  }
}
