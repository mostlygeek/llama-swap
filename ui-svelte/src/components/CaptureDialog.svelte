<script lang="ts">
  import type { ReqRespCapture } from "../lib/types";

  interface Props {
    capture: ReqRespCapture | null;
    open: boolean;
    onclose: () => void;
  }

  let { capture, open, onclose }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();

  type BodyTab = "raw" | "pretty" | "chat";
  let reqBodyTab: BodyTab = $state("pretty");
  let respBodyTab: BodyTab = $state("pretty");
  let copiedReq = $state(false);
  let copiedResp = $state(false);

  $effect(() => {
    if (open && dialogEl) {
      dialogEl.showModal();
    } else if (!open && dialogEl) {
      dialogEl.close();
    }
  });

  // Reset tabs when capture changes
  $effect(() => {
    if (capture) {
      const reqCt = getContentType(capture.req_headers);
      const respCt = getContentType(capture.resp_headers);
      reqBodyTab = reqCt.includes("json") ? "pretty" : "raw";
      respBodyTab = isSSE && (renderedResponse.reasoning || renderedResponse.content)
        ? "chat"
        : respCt.includes("json")
          ? "pretty"
          : "raw";
    }
  });

  function handleDialogClose() {
    onclose();
  }

  function decodeBody(body: string | null | undefined): string {
    if (!body) return "";
    try {
      const binary = atob(body);
      const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
      return new TextDecoder().decode(bytes);
    } catch {
      return body;
    }
  }

  function formatJson(str: string): string {
    try {
      const parsed = JSON.parse(str);
      return JSON.stringify(parsed, null, 2);
    } catch {
      return str;
    }
  }

  function getContentType(
    headers: Record<string, string> | null | undefined,
  ): string {
    if (!headers) return "";
    const ct = headers["Content-Type"] || headers["content-type"] || "";
    return ct.toLowerCase();
  }

  function isImageContentType(contentType: string): boolean {
    return contentType.startsWith("image/");
  }

  function isTextContentType(contentType: string): boolean {
    return (
      contentType.startsWith("text/") ||
      contentType.includes("application/json") ||
      contentType.includes("application/xml") ||
      contentType.includes("application/javascript")
    );
  }

  function getImageDataUrl(body: string, contentType: string): string {
    const mimeType = contentType.split(";")[0].trim();
    return `data:${mimeType};base64,${body}`;
  }

  interface RenderedResponse {
    reasoning: string;
    content: string;
  }

  function appendResponseOutput(
    result: RenderedResponse,
    response: unknown,
  ): RenderedResponse {
    const next = { ...result };
    const record = response as
      | {
          output?: Array<{
            type?: string;
            content?: Array<{ type?: string; text?: string }>;
          }>;
          choices?: Array<{
            message?: {
              content?: string | Array<{ type?: string; text?: string }>;
              reasoning_content?: string;
            };
          }>;
          output_text?: string;
        }
      | undefined;

    if (!record) return next;

    if (typeof record.output_text === "string") {
      next.content += record.output_text;
    }

    for (const choice of record.choices || []) {
      const message = choice.message;
      if (!message) continue;
      if (typeof message.reasoning_content === "string") {
        next.reasoning += message.reasoning_content;
      }
      if (typeof message.content === "string") {
        next.content += message.content;
      } else {
        for (const part of message.content || []) {
          if (part?.type === "text" && typeof part.text === "string") {
            next.content += part.text;
          }
        }
      }
    }

    for (const item of record.output || []) {
      if (item?.type === "reasoning") {
        for (const part of item.content || []) {
          if (typeof part?.text === "string") {
            next.reasoning += part.text;
          }
        }
      }
      if (item?.type === "message") {
        for (const part of item.content || []) {
          if (part?.type === "output_text" && typeof part.text === "string") {
            next.content += part.text;
          }
        }
      }
    }

    return next;
  }

  function parseSSEChat(text: string): RenderedResponse {
    const result: RenderedResponse = { reasoning: "", content: "" };
    const seenReasoningDeltaItems = new Set<string>();
    const seenContentDeltaItems = new Set<string>();

    for (const line of text.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed || !trimmed.startsWith("data: ")) continue;
      const data = trimmed.slice(6);
      if (data === "[DONE]") continue;
      try {
        const parsed = JSON.parse(data);
        const delta = parsed.choices?.[0]?.delta;
        if (delta?.content) result.content += delta.content;
        if (delta?.reasoning_content) result.reasoning += delta.reasoning_content;

        const eventType = parsed.event || parsed.type;
        if (
          eventType === "response.reasoning_text.delta" &&
          typeof parsed.data?.delta === "string"
        ) {
          result.reasoning += parsed.data.delta;
          if (typeof parsed.data?.item_id === "string") {
            seenReasoningDeltaItems.add(parsed.data.item_id);
          }
        }
        if (
          eventType === "response.output_text.delta" &&
          typeof parsed.data?.delta === "string"
        ) {
          result.content += parsed.data.delta;
          if (typeof parsed.data?.item_id === "string") {
            seenContentDeltaItems.add(parsed.data.item_id);
          }
        }
        if (
          eventType === "response.output_text.done" &&
          typeof parsed.data?.text === "string" &&
          typeof parsed.data?.item_id === "string" &&
          !seenContentDeltaItems.has(parsed.data.item_id)
        ) {
          result.content += parsed.data.text;
        }
        if (
          eventType === "response.completed" &&
          parsed.data?.response &&
          !result.reasoning &&
          !result.content
        ) {
          return appendResponseOutput(result, parsed.data.response);
        }
      } catch {
        // skip unparseable lines
      }
    }
    return result;
  }

  async function copyToClipboard(text: string, type: "req" | "resp") {
    try {
      await navigator.clipboard.writeText(text);
      if (type === "req") {
        copiedReq = true;
        setTimeout(() => (copiedReq = false), 1500);
      } else {
        copiedResp = true;
        setTimeout(() => (copiedResp = false), 1500);
      }
    } catch {
      // ignore
    }
  }

  function getCopyText(): string {
    if (respBodyTab === "chat") {
      let text = "";
      if (renderedResponse.reasoning) text += renderedResponse.reasoning + "\n\n";
      text += renderedResponse.content;
      return text;
    }
    return displayedResponseBody;
  }

  // Request body derivations
  let requestContentType = $derived(
    capture ? getContentType(capture.req_headers) : "",
  );
  let isRequestJson = $derived(requestContentType.includes("json"));

  let requestBodyRaw = $derived.by(() => {
    if (!capture) return "";
    return decodeBody(capture.req_body);
  });

  let requestBodyPretty = $derived.by(() => {
    if (!isRequestJson) return requestBodyRaw;
    return formatJson(requestBodyRaw);
  });

  let displayedRequestBody = $derived(
    reqBodyTab === "pretty" ? requestBodyPretty : requestBodyRaw,
  );

  // Response body derivations
  let responseContentType = $derived(
    capture ? getContentType(capture.resp_headers) : "",
  );
  let isResponseImage = $derived(isImageContentType(responseContentType));
  let isResponseText = $derived(isTextContentType(responseContentType));
  let isResponseJson = $derived(responseContentType.includes("json"));
  let isSSE = $derived(responseContentType.includes("text/event-stream"));

  let responseBodyRaw = $derived.by(() => {
    if (!capture) return "";
    return decodeBody(capture.resp_body);
  });

  let responseBodyPretty = $derived.by(() => {
    if (!isResponseJson) return responseBodyRaw;
    return formatJson(responseBodyRaw);
  });

  let renderedResponse = $derived.by(() => {
    if (!responseBodyRaw) return { reasoning: "", content: "" } as RenderedResponse;
    if (isSSE) return parseSSEChat(responseBodyRaw);
    return { reasoning: "", content: "" } as RenderedResponse;
  });

  let displayedResponseBody = $derived.by(() => {
    if (respBodyTab === "pretty") {
      return responseBodyPretty;
    }
    return responseBodyRaw;
  });
</script>

<dialog
  bind:this={dialogEl}
  onclose={handleDialogClose}
  class="bg-surface text-txtmain rounded-lg shadow-xl max-w-4xl w-full max-h-[90vh] p-0 backdrop:bg-black/50 m-auto"
>
  {#if capture}
    <div class="flex flex-col max-h-[90vh]">
      <div
        class="flex justify-between items-center p-4 border-b border-card-border"
      >
        <h2 class="text-xl font-bold pb-0">Capture #{capture.id + 1}{#if capture.req_path} <span class="text-base font-mono font-normal text-txtsecondary">{capture.req_path}</span>{/if}</h2>
        <button
          onclick={() => dialogEl?.close()}
          class="text-txtsecondary hover:text-txtmain text-2xl leading-none"
        >
          &times;
        </button>
      </div>

      <div class="overflow-y-auto flex-1 p-4 space-y-4">
        <!-- Request Headers -->
        <details class="group" open>
          <summary
            class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain"
          >
            Request Headers
          </summary>
          <div
            class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-48"
          >
            <table class="w-full text-sm">
              <tbody>
                {#each Object.entries(capture.req_headers || {}) as [key, value]}
                  <tr class="border-b border-card-border-inner last:border-0">
                    <td class="px-3 py-1 font-mono text-primary whitespace-nowrap"
                      >{key}</td
                    >
                    <td class="px-3 py-1 font-mono break-all">{value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>

        <!-- Request Body -->
        <details class="group" open>
          <summary
            class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain"
          >
            Request Body
          </summary>
          {#if requestBodyRaw}
            <div class="mt-2 flex items-center justify-between">
              <div class="flex gap-1">
                {#if isRequestJson}
                  <button
                    class="tab-btn"
                    class:tab-btn-active={reqBodyTab === "pretty"}
                    onclick={() => (reqBodyTab = "pretty")}>Pretty</button
                  >
                  <button
                    class="tab-btn"
                    class:tab-btn-active={reqBodyTab === "raw"}
                    onclick={() => (reqBodyTab = "raw")}>Raw</button
                  >
                {/if}
              </div>
              <button
                class="tab-btn"
                onclick={() =>
                  copyToClipboard(displayedRequestBody, "req")}
              >
                {#if copiedReq}
                  Copied!
                {:else}
                  Copy
                {/if}
              </button>
            </div>
            <div
              class="mt-1 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              <pre
                class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{displayedRequestBody}</pre>
            </div>
          {:else}
            <div
              class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              <pre class="p-3 text-sm font-mono whitespace-pre-wrap break-all"
                >(empty)</pre
              >
            </div>
          {/if}
        </details>

        <!-- Response Headers -->
        <details class="group" open>
          <summary
            class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain"
          >
            Response Headers
          </summary>
          <div
            class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-48"
          >
            <table class="w-full text-sm">
              <tbody>
                {#each Object.entries(capture.resp_headers || {}) as [key, value]}
                  <tr class="border-b border-card-border-inner last:border-0">
                    <td class="px-3 py-1 font-mono text-primary whitespace-nowrap"
                      >{key}</td
                    >
                    <td class="px-3 py-1 font-mono break-all">{value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>

        <!-- Response Body -->
        <details class="group" open>
          <summary
            class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain"
          >
            Response Body
          </summary>
          {#if isResponseImage && capture.resp_body}
            <div
              class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              <div class="p-3 flex justify-center">
                <img
                  src={getImageDataUrl(capture.resp_body, responseContentType)}
                  alt="Response"
                  class="max-w-full h-auto"
                />
              </div>
            </div>
          {:else if isSSE || isResponseText}
            <div class="mt-2 flex items-center justify-between">
              <div class="flex gap-1">
                {#if isSSE && (renderedResponse.reasoning || renderedResponse.content)}
                  <button
                    class="tab-btn"
                    class:tab-btn-active={respBodyTab === "chat"}
                    onclick={() => (respBodyTab = "chat")}>Chat</button
                  >
                {/if}
                {#if isResponseJson}
                  <button
                    class="tab-btn"
                    class:tab-btn-active={respBodyTab === "pretty"}
                    onclick={() => (respBodyTab = "pretty")}>Pretty</button
                  >
                {/if}
                {#if isSSE || isResponseJson}
                  <button
                    class="tab-btn"
                    class:tab-btn-active={respBodyTab === "raw"}
                    onclick={() => (respBodyTab = "raw")}>Raw</button
                  >
                {/if}
              </div>
              <button
                class="tab-btn"
                onclick={() => copyToClipboard(getCopyText(), "resp")}
              >
                {#if copiedResp}
                  Copied!
                {:else}
                  Copy
                {/if}
              </button>
            </div>
            <div
              class="mt-1 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              {#if respBodyTab === "chat"}
                <div class="p-3 text-sm space-y-3">
                  {#if renderedResponse.reasoning}
                    <div>
                      <div
                        class="text-xs font-semibold uppercase tracking-wider text-txtsecondary mb-1"
                      >
                        Reasoning
                      </div>
                      <pre
                        class="font-mono whitespace-pre-wrap break-all text-txtsecondary">{renderedResponse.reasoning}</pre>
                    </div>
                  {/if}
                  {#if renderedResponse.content}
                    <div>
                      {#if renderedResponse.reasoning}
                        <div
                          class="text-xs font-semibold uppercase tracking-wider text-txtsecondary mb-1"
                        >
                          Response
                        </div>
                      {/if}
                      <pre
                        class="font-mono whitespace-pre-wrap break-all">{renderedResponse.content}</pre>
                    </div>
                  {/if}
                </div>
              {:else}
                <pre
                  class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{displayedResponseBody || "(empty)"}</pre>
              {/if}
            </div>
          {:else if responseBodyRaw}
            <div
              class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              <div class="p-3 text-sm text-txtsecondary italic">
                (binary data - {responseContentType || "unknown content type"})
              </div>
            </div>
          {:else}
            <div
              class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96"
            >
              <pre class="p-3 text-sm font-mono">(empty)</pre>
            </div>
          {/if}
        </details>
      </div>

      <div class="p-4 border-t border-card-border flex justify-end">
        <button onclick={() => dialogEl?.close()} class="btn"> Close </button>
      </div>
    </div>
  {/if}
</dialog>

<style>
  .tab-btn {
    padding: 2px 10px;
    font-size: 0.75rem;
    border-radius: 4px;
    color: var(--color-txtsecondary);
    cursor: pointer;
    border: 1px solid transparent;
    background: transparent;
    transition: all 0.15s;
  }
  .tab-btn:hover {
    color: var(--color-txtmain);
    background: var(--color-secondary);
  }
  .tab-btn-active {
    color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 12%, transparent);
    border-color: color-mix(in srgb, var(--color-primary) 25%, transparent);
  }
</style>
