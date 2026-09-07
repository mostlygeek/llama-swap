import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatOptions, StreamChunk } from "./chatApi";

// chatApi remembers, per module instance, whether the backend rejected the
// llama.cpp timings extension. Each test gets a fresh instance.
let streamChatCompletion: typeof import("./chatApi").streamChatCompletion;
beforeEach(async () => {
  vi.resetModules();
  vi.unstubAllGlobals();
  ({ streamChatCompletion } = await import("./chatApi"));
});

/** A streamed response body carrying the given SSE lines. */
function sseResponse(lines: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      const encoder = new TextEncoder();
      for (const line of lines) controller.enqueue(encoder.encode(line + "\n"));
      controller.close();
    },
  });
  return { ok: true, status: 200, body } as unknown as Response;
}

const badRequest = (text: string) => ({ ok: false, status: 400, text: async () => text }) as Response;
const bodyOf = (call: unknown[]) => JSON.parse((call[1] as RequestInit).body as string);
const okStream = () => sseResponse([`data: {"choices":[{"delta":{"content":"ok"}}]}`, "data: [DONE]"]);

async function collect(options?: ChatOptions): Promise<StreamChunk[]> {
  const out: StreamChunk[] = [];
  for await (const chunk of streamChatCompletion("m", [{ role: "user", content: "hi" }], undefined, options)) {
    out.push(chunk);
  }
  return out;
}

describe("streamChatCompletion", () => {
  it("asks for per-chunk timings and streams the reply", async () => {
    const fetchMock = vi.fn().mockResolvedValue(okStream());
    vi.stubGlobal("fetch", fetchMock);

    const chunks = await collect();

    expect(bodyOf(fetchMock.mock.calls[0]).timings_per_token).toBe(true);
    expect(chunks[0]).toMatchObject({ content: "ok" });
  });

  // A backend that validates its request body rejects the llama.cpp
  // extension by name; the turn must still go through, and later turns must
  // not pay for the rejection again.
  it("retries without the extension when the backend rejects it by name, then stops sending it", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(badRequest("Unrecognized request argument supplied: timings_per_token"))
      .mockResolvedValue(okStream());
    vi.stubGlobal("fetch", fetchMock);

    expect((await collect()).some((c) => c.content === "ok")).toBe(true);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(bodyOf(fetchMock.mock.calls[0])).toHaveProperty("timings_per_token");
    expect(bodyOf(fetchMock.mock.calls[1])).not.toHaveProperty("timings_per_token");

    await collect();
    expect(fetchMock).toHaveBeenCalledTimes(3); // one request, no retry
    expect(bodyOf(fetchMock.mock.calls[2])).not.toHaveProperty("timings_per_token");
  });

  // An ordinary validation error says nothing about the extension: it is
  // reported at once, costs no second request, and leaves the extension on.
  it("reports a 400 that does not name the extension without retrying or giving up on it", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(badRequest("model not found")).mockResolvedValue(okStream());
    vi.stubGlobal("fetch", fetchMock);

    await expect(collect()).rejects.toThrow(/400 - model not found/);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await collect();
    expect(bodyOf(fetchMock.mock.calls[1]).timings_per_token).toBe(true);
  });

  it("reports the second failure when the retry fails too", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(badRequest("unknown field timings_per_token"))
      .mockResolvedValueOnce({ ok: false, status: 500, text: async () => "boom" } as Response);
    vi.stubGlobal("fetch", fetchMock);

    await expect(collect()).rejects.toThrow(/500 - boom/);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("does not send the extension to the other endpoints", async () => {
    const fetchMock = vi.fn().mockResolvedValue(sseResponse(["event: message_stop", "data: {}", ""]));
    vi.stubGlobal("fetch", fetchMock);

    await collect({ endpoint: "v1/messages" });
    expect(bodyOf(fetchMock.mock.calls[0])).not.toHaveProperty("timings_per_token");
  });
});

describe("stop reasons on the other endpoints", () => {
  const event = (name: string, data: object) => [`event: ${name}`, `data: ${JSON.stringify(data)}`, ""];

  it("reads usage and a normalized stop reason from an Anthropic stream", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        sseResponse([
          ...event("message_start", { message: { usage: { input_tokens: 12 } } }),
          ...event("content_block_delta", { delta: { type: "text_delta", text: "hi" } }),
          ...event("message_delta", { delta: { stop_reason: "max_tokens" }, usage: { output_tokens: 7 } }),
          ...event("message_stop", {}),
        ])
      )
    );

    const chunks = await collect({ endpoint: "v1/messages" });
    expect(chunks.find((c) => c.usage?.prompt_tokens)?.usage).toEqual({ prompt_tokens: 12 });
    expect(chunks.find((c) => c.finish_reason)).toMatchObject({ finish_reason: "length", usage: { completion_tokens: 7 } });
    expect(chunks.at(-1)?.done).toBe(true);
  });

  it("reads a completed Responses stream as stop", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        sseResponse([
          ...event("response.output_text.delta", { delta: "hi" }),
          ...event("response.completed", { response: { status: "completed", usage: { input_tokens: 3, output_tokens: 1 } } }),
        ])
      )
    );

    const chunks = await collect({ endpoint: "v1/responses" });
    expect(chunks.find((c) => c.finish_reason)).toMatchObject({
      finish_reason: "stop",
      usage: { prompt_tokens: 3, completion_tokens: 1 },
    });
  });

  it("reads an incomplete Responses stream as length and still ends the turn", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        sseResponse([
          ...event("response.output_text.delta", { delta: "hi" }),
          ...event("response.incomplete", {
            response: { status: "incomplete", incomplete_details: { reason: "max_output_tokens" } },
          }),
        ])
      )
    );

    const chunks = await collect({ endpoint: "v1/responses" });
    expect(chunks.find((c) => c.finish_reason)?.finish_reason).toBe("length");
    expect(chunks.at(-1)?.done).toBe(true);
  });
});
