<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import * as Tabs from "$lib/components/ui/tabs/index.js";
  import { copyText } from "../../lib/clipboard";
  import { copyPngToClipboard, downloadPngBlob } from "../../lib/tableImage";

  interface Props {
    markdown: string;
    open: boolean;
    onclose: () => void;
    /** Produces the table's PNG on demand (JIT). The Screenshot tab and its
     *  buttons are hidden when it is not provided. */
    captureScreenshot?: () => Promise<Blob>;
    /** File name (without extension) used for the PNG download. */
    fileName?: string;
  }

  let { markdown, open, onclose, captureScreenshot, fileName = "table" }: Props = $props();

  let contentEl = $state<HTMLElement | null>(null);
  let preEl = $state<HTMLPreElement | null>(null);
  let tab = $state<"markdown" | "screenshot">("markdown");

  let status = $state<"idle" | "copied" | "failed">("idle");
  let statusTimer: ReturnType<typeof setTimeout> | null = null;

  // The screenshot is rendered JIT: only when the Screenshot tab is shown, and
  // the result is cached for the life of the open dialog.
  let imageStatus = $state<"idle" | "busy" | "done" | "failed">("idle");
  let imageTimer: ReturnType<typeof setTimeout> | null = null;
  let imageUrl = $state<string | null>(null);
  let copyHint = $state(false);
  let captured = $state(false);
  let blobCache: Blob | null = null;

  // execCommand("copy") is the only route on plain http, and it can still be
  // refused. When it is, select the source so the user can finish the job with
  // Ctrl+C rather than leaving them with a button that silently does nothing.
  async function copy() {
    const copied = await copyText(markdown, contentEl);
    status = copied ? "copied" : "failed";
    if (!copied) selectSource();
    if (statusTimer !== null) clearTimeout(statusTimer);
    statusTimer = setTimeout(() => {
      status = "idle";
      statusTimer = null;
    }, 2000);
  }

  function selectSource() {
    if (!preEl) return;
    const range = document.createRange();
    range.selectNodeContents(preEl);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
  }

  function flashImageStatus(next: "done" | "failed") {
    if (imageTimer !== null) clearTimeout(imageTimer);
    imageStatus = next;
    imageTimer = setTimeout(() => {
      imageStatus = "idle";
      imageTimer = null;
    }, 2000);
  }

  async function ensureBlob(): Promise<Blob> {
    if (!captureScreenshot) throw new Error("No screenshot source");
    if (!blobCache) blobCache = await captureScreenshot();
    return blobCache;
  }

  // JIT: render the screenshot the first time the Screenshot tab is shown while
  // the dialog is open; reuse the cached blob for subsequent actions.
  $effect(() => {
    if (!(open && tab === "screenshot" && captureScreenshot && !captured)) return;
    let cancelled = false;
    ensureBlob()
      .then((blob) => {
        if (cancelled) return;
        captured = true;
        if (imageUrl) URL.revokeObjectURL(imageUrl);
        imageUrl = URL.createObjectURL(blob);
      })
      .catch((err) => {
        console.error("Failed to render screenshot:", err);
        if (!cancelled) flashImageStatus("failed");
      });
    return () => {
      cancelled = true;
    };
  });

  async function copyImage() {
    if (!captureScreenshot || imageStatus === "busy") return;
    imageStatus = "busy";
    try {
      const blob = await ensureBlob();
      const copied = await copyPngToClipboard(blob);
      if (copied) {
        copyHint = false;
        flashImageStatus("done");
      } else {
        // Programmatic copy needs a secure context. Fall back to the preview:
        // right-click the image to copy or save it.
        copyHint = true;
        flashImageStatus("failed");
      }
    } catch (err) {
      console.error("Failed to copy image:", err);
      copyHint = true;
      flashImageStatus("failed");
    }
  }

  async function downloadImage() {
    if (!captureScreenshot || imageStatus === "busy") return;
    imageStatus = "busy";
    try {
      const blob = await ensureBlob();
      downloadPngBlob(blob, `${fileName}.png`);
      flashImageStatus("done");
    } catch (err) {
      console.error("Failed to download image:", err);
      flashImageStatus("failed");
    }
  }

  // Start each open from a clean slate: default to Markdown and drop the cached
  // screenshot so a re-open re-renders the current table. This depends only on
  // `open` -- writing the state variables below does not create a dependency on
  // them, so a later tab switch or render does not re-trigger this reset.
  $effect(() => {
    if (open) {
      tab = "markdown";
      captured = false;
      blobCache = null;
      imageUrl = null;
      imageStatus = "idle";
      copyHint = false;
    }
  });

  // Release the object URL whenever it changes or on teardown, so a superseded
  // or closed-dialog URL is never left dangling.
  $effect(() => {
    const url = imageUrl;
    return () => {
      if (url) URL.revokeObjectURL(url);
    };
  });

  $effect(() => {
    return () => {
      if (statusTimer !== null) clearTimeout(statusTimer);
      if (imageTimer !== null) clearTimeout(imageTimer);
    };
  });
</script>

<Dialog.Root
  {open}
  onOpenChange={(v) => {
    if (!v) onclose();
  }}
>
  <Dialog.Content
    bind:ref={contentEl}
    class="flex max-h-[90vh] w-[90%] flex-col gap-0 p-0 sm:max-w-[90%]"
  >
    <Dialog.Header class="border-border border-b px-4 py-3">
      <Dialog.Title class="text-lg font-bold">Export</Dialog.Title>
      <Dialog.Description class="text-muted-foreground text-sm">
        The current page of results as markdown source or a screenshot.
      </Dialog.Description>
    </Dialog.Header>

    <Tabs.Root bind:value={tab} class="min-h-0 flex-1">
      <div class="px-4 pt-3">
        <Tabs.List>
          <Tabs.Trigger value="markdown">Markdown</Tabs.Trigger>
          <Tabs.Trigger value="screenshot" disabled={!captureScreenshot}>
            Screenshot
          </Tabs.Trigger>
        </Tabs.List>
      </div>

      <Tabs.Content value="markdown" class="overflow-auto px-4 py-3">
        <div class="bg-background border-border max-h-[50vh] overflow-auto rounded-md border">
          <pre
            bind:this={preEl}
            class="p-3 font-mono text-xs whitespace-pre">{markdown || "(no rows)"}</pre>
        </div>
      </Tabs.Content>

      <Tabs.Content value="screenshot" class="overflow-auto px-4 py-3">
        <div class="bg-background border-border max-h-[50vh] overflow-auto rounded-md border">
          {#if imageUrl}
            <img src={imageUrl} alt="Table screenshot" class="block max-w-full" />
          {:else if imageStatus === "failed"}
            <div class="text-muted-foreground p-6 text-sm">Failed to render the screenshot.</div>
          {:else}
            <div class="text-muted-foreground p-6 text-sm">Rendering…</div>
          {/if}
        </div>
        {#if copyHint}
          <p class="text-muted-foreground mt-2 text-xs">
            Programmatic copy needs a secure context (https). Right-click the
            image and choose "Copy image" or "Save image as".
          </p>
        {/if}
      </Tabs.Content>
    </Tabs.Root>

    <!--
      Dialog.Footer's -mx-4/-mb-4 cancel the default p-4 on Dialog.Content. This
      dialog sets p-0, so those offsets have to be zeroed or the bar hangs off
      the edges of the box.
    -->
    <Dialog.Footer
      class="border-border bg-card mx-0 mb-0 flex flex-wrap items-center gap-2 border-t px-4 py-3 sm:justify-end"
    >
      <Button variant="outline" onclick={onclose}>Close</Button>
      {#if tab === "markdown"}
        <Button onclick={copy} disabled={!markdown}>
          {status === "copied" ? "Copied!" : status === "failed" ? "Press Ctrl+C" : "Copy markdown"}
        </Button>
      {:else}
        <Button variant="outline" onclick={downloadImage} disabled={!captureScreenshot || imageStatus === "busy"}>
          {imageStatus === "busy" ? "Working…" : "Download image"}
        </Button>
        <Button variant="outline" onclick={copyImage} disabled={!captureScreenshot || imageStatus === "busy"}>
          {imageStatus === "done"
            ? "Copied!"
            : imageStatus === "failed"
              ? "Copy failed"
              : "Copy image"}
        </Button>
      {/if}
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
