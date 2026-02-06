<script lang="ts">
  import type { ReqRespCapture } from "../lib/types";

  interface Props {
    capture: ReqRespCapture | null;
    open: boolean;
    onclose: () => void;
  }

  let { capture, open, onclose }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();

  $effect(() => {
    if (open && dialogEl) {
      dialogEl.showModal();
    } else if (!open && dialogEl) {
      dialogEl.close();
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

  function getContentType(headers: Record<string, string> | null | undefined): string {
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

  let requestBody = $derived.by(() => {
    if (!capture) return "";
    const decoded = decodeBody(capture.req_body);
    const ct = getContentType(capture.req_headers);
    if (ct.includes("json")) {
      return formatJson(decoded);
    }
    return decoded;
  });

  let responseBody = $derived.by(() => {
    if (!capture) return "";
    return decodeBody(capture.resp_body);
  });

  let responseContentType = $derived(capture ? getContentType(capture.resp_headers) : "");
  let isResponseImage = $derived(isImageContentType(responseContentType));
  let isResponseText = $derived(isTextContentType(responseContentType));

  let formattedResponseBody = $derived.by(() => {
    if (!isResponseText) return "";
    if (responseContentType.includes("json")) {
      return formatJson(responseBody);
    }
    return responseBody;
  });
</script>

<dialog
  bind:this={dialogEl}
  onclose={handleDialogClose}
  class="bg-surface text-txtmain rounded-lg shadow-xl max-w-4xl w-full max-h-[90vh] p-0 backdrop:bg-black/50 m-auto"
>
  {#if capture}
    <div class="flex flex-col max-h-[90vh]">
      <div class="flex justify-between items-center p-4 border-b border-card-border">
        <h2 class="text-xl font-bold pb-0">Capture #{capture.id + 1}</h2>
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
          <summary class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain">
            Request Headers
          </summary>
          <div class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-48">
            <table class="w-full text-sm">
              <tbody>
                {#each Object.entries(capture.req_headers || {}) as [key, value]}
                  <tr class="border-b border-card-border-inner last:border-0">
                    <td class="px-3 py-1 font-mono text-primary whitespace-nowrap">{key}</td>
                    <td class="px-3 py-1 font-mono break-all">{value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>

        <!-- Request Body -->
        <details class="group" open>
          <summary class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain">
            Request Body
          </summary>
          <div class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96">
            <pre class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{requestBody || "(empty)"}</pre>
          </div>
        </details>

        <!-- Response Headers -->
        <details class="group" open>
          <summary class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain">
            Response Headers
          </summary>
          <div class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-48">
            <table class="w-full text-sm">
              <tbody>
                {#each Object.entries(capture.resp_headers || {}) as [key, value]}
                  <tr class="border-b border-card-border-inner last:border-0">
                    <td class="px-3 py-1 font-mono text-primary whitespace-nowrap">{key}</td>
                    <td class="px-3 py-1 font-mono break-all">{value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        </details>

        <!-- Response Body -->
        <details class="group" open>
          <summary class="cursor-pointer font-semibold text-sm uppercase tracking-wider text-txtsecondary hover:text-txtmain">
            Response Body
          </summary>
          <div class="mt-2 bg-background rounded border border-card-border overflow-auto max-h-96">
            {#if isResponseImage && capture.resp_body}
              <div class="p-3 flex justify-center">
                <img
                  src={getImageDataUrl(capture.resp_body, responseContentType)}
                  alt="Response"
                  class="max-w-full h-auto"
                />
              </div>
            {:else if isResponseText}
              <pre class="p-3 text-sm font-mono whitespace-pre-wrap break-all">{formattedResponseBody || "(empty)"}</pre>
            {:else if responseBody}
              <div class="p-3 text-sm text-txtsecondary italic">
                (binary data - {responseContentType || "unknown content type"})
              </div>
            {:else}
              <pre class="p-3 text-sm font-mono">(empty)</pre>
            {/if}
          </div>
        </details>
      </div>

      <div class="p-4 border-t border-card-border flex justify-end">
        <button onclick={() => dialogEl?.close()} class="btn">
          Close
        </button>
      </div>
    </div>
  {/if}
</dialog>
