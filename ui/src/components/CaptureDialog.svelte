<script lang="ts">
  import type { ActivityLogEntry, ReqRespCapture } from "../lib/types";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import * as Tabs from "$lib/components/ui/tabs/index.js";
  import { copyText } from "../lib/clipboard";
  import {
    formatAbsoluteTime,
    formatDuration,
    formatFileSize,
    formatSpeed,
    formatTokenCount,
  } from "../lib/format";

  interface Props {
    capture: ReqRespCapture | null;
    /** The activity row the capture belongs to; shown on the Summary tab. */
    activity?: ActivityLogEntry | null;
    open: boolean;
    onclose: () => void;
  }

  let { capture, activity = null, open, onclose }: Props = $props();

  // The clipboard fallback used on plain http has to live inside the dialog;
  // see copyText.
  let contentEl = $state<HTMLElement | null>(null);

  type DialogTab = "summary" | "request" | "response";
  let dialogTab: DialogTab = $state("summary");

  type BodyTab = "raw" | "pretty" | "chat";
  let reqBodyTab: BodyTab = $state("pretty");
  let respBodyTab: BodyTab = $state("pretty");
  let copiedReq = $state(false);
  let copiedResp = $state(false);

  // Reset tabs when capture changes
  $effect(() => {
    if (capture) {
      dialogTab = "summary";
      const reqCt = getContentType(capture.req_headers);
      const respCt = getContentType(capture.resp_headers);
      reqBodyTab = reqCt.includes("json") ? "pretty" : "raw";
      respBodyTab = respCt.includes("text/event-stream")
        ? "chat"
        : respCt.includes("json")
          ? "pretty"
          : "raw";
    }
  });

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

  /** Byte length of a base64 payload, used for the summary sizes. */
  function base64ByteLength(body: string | null | undefined): number {
    if (!body) return 0;
    let padding = 0;
    if (body.endsWith("==")) padding = 2;
    else if (body.endsWith("=")) padding = 1;
    return Math.max(0, Math.floor((body.length * 3) / 4) - padding);
  }

  interface SSEChat {
    reasoning: string;
    content: string;
  }

  function parseSSEChat(text: string): SSEChat {
    const result: SSEChat = { reasoning: "", content: "" };
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
        if (delta?.reasoning) result.reasoning += delta.reasoning;
      } catch {
        // skip unparseable lines
      }
    }
    return result;
  }

  async function copyToClipboard(text: string, type: "req" | "resp") {
    if (!(await copyText(text, contentEl))) return;
    if (type === "req") {
      copiedReq = true;
      setTimeout(() => (copiedReq = false), 1500);
    } else {
      copiedResp = true;
      setTimeout(() => (copiedResp = false), 1500);
    }
  }

  function getCopyText(): string {
    if (respBodyTab === "chat") {
      let text = "";
      if (sseChat.reasoning) text += sseChat.reasoning + "\n\n";
      text += sseChat.content;
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

  let sseChat = $derived.by(() => {
    if (!isSSE || !responseBodyRaw)
      return { reasoning: "", content: "" } as SSEChat;
    return parseSSEChat(responseBodyRaw);
  });

  let displayedResponseBody = $derived.by(() => {
    if (respBodyTab === "pretty") return responseBodyPretty;
    return responseBodyRaw;
  });

  // Summary derivations
  let reqHeaders = $derived(capture ? Object.entries(capture.req_headers || {}) : []);
  let respHeaders = $derived(capture ? Object.entries(capture.resp_headers || {}) : []);
  let reqBodySize = $derived(
    capture ? base64ByteLength(capture.req_body) : 0,
  );
  let respBodySize = $derived(
    capture ? base64ByteLength(capture.resp_body) : 0,
  );
  let metadataEntries = $derived(
    activity ? Object.entries(activity.metadata || {}) : [],
  );
  let tokenRows = $derived.by(() => {
    const t = activity?.tokens;
    if (!t) return [];
    const rows: [string, string][] = [
      ["Input", formatTokenCount(t.input_tokens)],
      ["Output", formatTokenCount(t.output_tokens)],
    ];
    // Optional counts are only reported by some servers; the server uses -1
    // for "not reported", so hide those rows rather than showing a negative.
    if (t.cache_tokens > 0) rows.push(["Cached", t.cache_tokens.toLocaleString()]);
    if (t.draft_tokens > 0) rows.push(["Draft", t.draft_tokens.toLocaleString()]);
    if (t.draft_acc_tokens > 0)
      rows.push(["Draft accepted", t.draft_acc_tokens.toLocaleString()]);
    rows.push(["Prefill speed", formatSpeed(t.prompt_per_second)]);
    rows.push(["Decode speed", formatSpeed(t.tokens_per_second)]);
    return rows;
  });

  function statusClass(code: number): string {
    if (code >= 500) return "text-destructive";
    if (code >= 400) return "text-yellow-500";
    if (code >= 200 && code < 300) return "text-green-600 dark:text-green-400";
    return "";
  }
</script>

<Dialog.Root
  {open}
  onOpenChange={(v) => {
    if (!v) onclose();
  }}
>
  <Dialog.Content
    bind:ref={contentEl}
    class="flex max-h-[90vh] w-[90%] sm:max-w-[90%] flex-col gap-0 p-0"
  >
    {#if capture}
      {#snippet headerTable(headers: [string, string][])}
        <div class="rounded-md border border-border">
          <table class="w-full text-sm">
            <tbody>
              {#each headers as [key, value]}
                <tr class="border-b border-border last:border-0">
                  <td
                    class="px-3 py-1 font-mono text-primary whitespace-nowrap align-top"
                  >{key}</td
                  >
                  <td class="px-3 py-1 font-mono break-all">{value}</td>
                </tr>
              {:else}
                <tr>
                  <td class="px-3 py-1 text-sm text-muted-foreground italic">
                    (no headers)
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/snippet}

      {#snippet field(label: string, value: string, valueClass = "")}
        <div class="min-w-0">
          <div
            class="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {label}
          </div>
          <div class="mt-0.5 font-mono break-all {valueClass}">{value}</div>
        </div>
      {/snippet}

      <Dialog.Header class="border-b border-border px-4 py-3">
        <Dialog.Title class="text-lg font-bold">
          Capture #{capture.id}{#if capture.req_path}
            <span class="font-mono text-base font-normal text-muted-foreground">{capture.req_path}</span>{/if}
        </Dialog.Title>
      </Dialog.Header>

      <Tabs.Root
        bind:value={dialogTab}
        class="min-h-0 flex-1 overflow-hidden"
      >
        <Tabs.List variant="line" class="mx-4 mt-2 border-b border-border">
          <Tabs.Trigger value="summary">Summary</Tabs.Trigger>
          <Tabs.Trigger value="request">Request</Tabs.Trigger>
          <Tabs.Trigger value="response">Response</Tabs.Trigger>
        </Tabs.List>

        <Tabs.Content
          value="summary"
          class="min-h-0 overflow-y-auto p-4 space-y-4"
        >
          {#if activity}
            <section class="grid grid-cols-2 gap-x-6 gap-y-3 md:grid-cols-4">
              {@render field("Time", formatAbsoluteTime(activity.timestamp))}
              {@render field("Source", activity.src || "-")}
              {@render field("Model", activity.model || "-")}
              {@render field("Task", activity.task || "-")}
              {@render field(
                "Status",
                String(activity.resp_status_code),
                statusClass(activity.resp_status_code),
              )}
              {@render field("Duration", formatDuration(activity.duration_ms))}
              {@render field("Response Type", activity.resp_content_type || "-")}
              {@render field("Request Path", activity.req_path || "-")}
            </section>

            <section>
              <div
                class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                Tokens
              </div>
              <div class="rounded-md border border-border">
                <table class="w-full text-sm">
                  <tbody>
                    {#each tokenRows as [label, value]}
                      <tr class="border-b border-border last:border-0">
                        <td
                          class="w-40 px-3 py-1 text-muted-foreground"
                        >{label}</td
                        >
                        <td class="px-3 py-1 font-mono">{value}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </section>

            {#if activity.error_msg}
              <section>
                <div
                  class="mb-1 text-xs font-semibold uppercase tracking-wider text-destructive"
                >
                  Error
                </div>
                <div
                  class="rounded-md border border-border bg-background p-3 font-mono whitespace-pre-wrap break-all"
                >
                  {activity.error_msg}
                </div>
              </section>
            {/if}

            {#if metadataEntries.length > 0}
              <section>
                <div
                  class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                >
                  Metadata
                </div>
                <div class="rounded-md border border-border">
                  <table class="w-full text-sm">
                    <tbody>
                      {#each metadataEntries as [key, value]}
                        <tr class="border-b border-border last:border-0">
                          <td
                            class="w-40 px-3 py-1 font-mono text-primary whitespace-nowrap"
                          >{key}</td
                          >
                          <td class="px-3 py-1 font-mono break-all">{value}</td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              </section>
            {/if}
          {:else}
            <p class="text-sm text-muted-foreground">
              No activity record is available for this capture.
            </p>
          {/if}

          <section>
            <div
              class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
            >
              Capture
            </div>
            <div class="rounded-md border border-border">
              <table class="w-full text-sm">
                <tbody>
                  <tr class="border-b border-border last:border-0">
                    <td class="w-40 px-3 py-1 text-muted-foreground">ID</td>
                    <td class="px-3 py-1 font-mono">{capture.id}</td>
                  </tr>
                  <tr class="border-b border-border last:border-0">
                    <td class="w-40 px-3 py-1 text-muted-foreground">
                      Request
                    </td>
                    <td class="px-3 py-1 font-mono break-all">
                      {reqHeaders.length} headers,
                      {reqBodySize ? formatFileSize(reqBodySize) : "no body"}
                    </td>
                  </tr>
                  <tr class="border-b border-border last:border-0">
                    <td class="w-40 px-3 py-1 text-muted-foreground">
                      Response
                    </td>
                    <td class="px-3 py-1 font-mono break-all">
                      {respHeaders.length} headers,
                      {respBodySize ? formatFileSize(respBodySize) : "no body"}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </Tabs.Content>

        <Tabs.Content
          value="request"
          class="min-h-0 overflow-y-auto p-4 space-y-4"
        >
          <section>
            <div
              class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
            >
              Endpoint
            </div>
            <div class="rounded-md border border-border bg-background p-3 font-mono break-all">
              {capture.req_path || "-"}
            </div>
          </section>

          <details class="group" open>
            <summary
              class="mb-1 cursor-pointer text-xs font-semibold uppercase tracking-wider text-muted-foreground hover:text-foreground"
            >
              Headers ({reqHeaders.length})
            </summary>
            {@render headerTable(reqHeaders)}
          </details>

          <section>
            <div class="mb-1 flex items-center justify-between">
              <div
                class="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                Body{#if reqBodySize} ({formatFileSize(reqBodySize)}){/if}
              </div>
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
                <button
                  class="tab-btn"
                  onclick={() => copyToClipboard(displayedRequestBody, "req")}
                >
                  {#if copiedReq}
                    Copied!
                  {:else}
                    Copy
                  {/if}
                </button>
              </div>
            </div>
            <div class="rounded-md border border-border bg-background">
              <pre
                class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{displayedRequestBody || "(empty)"}</pre
              >
            </div>
          </section>
        </Tabs.Content>

        <Tabs.Content
          value="response"
          class="min-h-0 overflow-y-auto p-4 space-y-4"
        >
          <section>
            <div
              class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
            >
              Status
            </div>
            <div class="rounded-md border border-border bg-background p-3 font-mono">
              <span class={statusClass(activity?.resp_status_code ?? 0)}>
                {activity ? activity.resp_status_code : "—"}
              </span>
              {#if activity?.duration_ms != null}
                <span class="text-muted-foreground">
                  · {formatDuration(activity.duration_ms)}</span
                >
              {/if}
            </div>
          </section>

          <details class="group" open>
            <summary
              class="mb-1 cursor-pointer text-xs font-semibold uppercase tracking-wider text-muted-foreground hover:text-foreground"
            >
              Headers ({respHeaders.length})
            </summary>
            {@render headerTable(respHeaders)}
          </details>

          <section>
            <div class="mb-1 flex items-center justify-between">
              <div
                class="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                Body{#if respBodySize} ({formatFileSize(respBodySize)}){/if}
              </div>
              {#if isResponseImage}
                <span></span>
              {:else}
                <div class="flex gap-1">
                  {#if isSSE}
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
              {/if}
            </div>

            {#if isResponseImage && capture.resp_body}
              <div class="rounded-md border border-border bg-background">
                <div class="flex justify-center p-3">
                  <img
                    src={getImageDataUrl(capture.resp_body, responseContentType)}
                    alt="Response"
                    class="h-auto max-w-full"
                  />
                </div>
              </div>
            {:else if isSSE || isResponseText}
              <div class="rounded-md border border-border bg-background">
                {#if respBodyTab === "chat"}
                  <div class="space-y-3 p-3 text-sm">
                    {#if sseChat.reasoning}
                      <div>
                        <div
                          class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                        >
                          Reasoning
                        </div>
                        <pre
                          class="font-mono whitespace-pre-wrap break-all text-muted-foreground">{sseChat.reasoning}</pre
                        >
                      </div>
                    {/if}
                    {#if sseChat.content}
                      <div>
                        {#if sseChat.reasoning}
                          <div
                            class="mb-1 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
                          >
                            Response
                          </div>
                        {/if}
                        <pre
                          class="font-mono whitespace-pre-wrap break-all">{sseChat.content}</pre
                        >
                      </div>
                    {/if}
                    {#if !sseChat.reasoning && !sseChat.content}
                      <pre class="font-mono">(empty)</pre>
                    {/if}
                  </div>
                {:else}
                  <pre
                    class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{displayedResponseBody || "(empty)"}</pre
                  >
                {/if}
              </div>
            {:else if responseBodyRaw}
              <div class="rounded-md border border-border bg-background">
                <div class="p-3 text-sm text-muted-foreground italic">
                  (binary data - {responseContentType || "unknown content type"})
                </div>
              </div>
            {:else}
              <div class="rounded-md border border-border bg-background">
                <pre class="p-3 text-sm font-mono">(empty)</pre>
              </div>
            {/if}
          </section>
        </Tabs.Content>
      </Tabs.Root>

      <Dialog.Footer class="border-t border-border px-4 py-3 sm:justify-end">
        <Button variant="outline" onclick={onclose}>Close</Button>
      </Dialog.Footer>
    {:else}
      <div class="flex flex-col items-center justify-center p-12">
        <p class="text-lg text-muted-foreground">Capture not found</p>
        <p class="text-sm text-muted-foreground mt-1">The capture may have expired or was never recorded.</p>
        <div class="mt-4">
          <Button variant="outline" onclick={onclose}>Close</Button>
        </div>
      </div>
    {/if}
  </Dialog.Content>
</Dialog.Root>

<style>
  .tab-btn {
    padding: 2px 10px;
    font-size: 0.75rem;
    border-radius: 0;
    color: var(--muted-foreground);
    cursor: pointer;
    border: 1px solid transparent;
    background: transparent;
    transition: all 0.15s;
  }
  .tab-btn:hover {
    color: var(--foreground);
    background: var(--accent);
  }
  .tab-btn-active {
    color: var(--primary);
    background: color-mix(in srgb, var(--primary) 12%, transparent);
    border-color: color-mix(in srgb, var(--primary) 25%, transparent);
  }
</style>
