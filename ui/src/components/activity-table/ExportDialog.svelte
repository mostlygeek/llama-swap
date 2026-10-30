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

  type Feedback = "idle" | "ok" | "failed";

  let { markdown, open, onclose, captureScreenshot, fileName = "table" }: Props = $props();

  let contentEl = $state<HTMLElement | null>(null);
  let preEl = $state<HTMLPreElement | null>(null);
  let tab = $state<"markdown" | "screenshot">("markdown");

  let status = $state<"idle" | "copied" | "failed">("idle");
  let statusTimer: ReturnType<typeof setTimeout> | null = null;

  // The screenshot is rendered JIT: only when the Screenshot tab is shown, and
  // the result is cached for the life of the open dialog.
  //
  // The preview state is kept apart from the button feedback below and is never
  // timed out: a render that failed stays failed until a retry succeeds, rather
  // than falling back to a "Rendering…" that can never finish.
  let preview = $state<"idle" | "busy" | "ready" | "failed">("idle");
  let actionBusy = $state(false);
  let imageUrl = $state<string | null>(null);
  let copyHint = $state(false);
  let captured = $state(false);

  // Per-action feedback, so a successful download does not put "Copied!" on the
  // Copy button.
  let downloadFeedback = $state<Feedback>("idle");
  let copyFeedback = $state<Feedback>("idle");
  let feedbackTimers: Partial<Record<"download" | "copy", ReturnType<typeof setTimeout>>> = {};

  // Either the JIT preview render or a copy/download is in flight.
  const busy = $derived(preview === "busy" || actionBusy);
  let blobCache: Blob | null = null;
  // One capture shared by every caller. The preview's JIT render and a
  // Download/Copy click can overlap, and rasterizing the table twice in
  // parallel would double the work and fight over the table's columns.
  let capturePending: Promise<Blob> | null = null;
  // Bumped when the dialog re-opens. Results from an older capture are
  // dropped instead of cached, so a re-open always renders the current table.
  let captureSeq = 0;

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

  function flashFeedback(which: "download" | "copy", next: Feedback) {
    const existing = feedbackTimers[which];
    if (existing !== undefined) clearTimeout(existing);
    if (which === "download") downloadFeedback = next;
    else copyFeedback = next;
    feedbackTimers[which] = setTimeout(() => {
      if (which === "download") downloadFeedback = "idle";
      else copyFeedback = "idle";
      delete feedbackTimers[which];
    }, 2000);
  }

  function actionLabel(which: "download" | "copy"): string {
    if (busy) return "Working…";
    if (which === "download") {
      if (downloadFeedback === "ok") return "Saved!";
      return downloadFeedback === "failed" ? "Save failed" : "Download image";
    }
    if (copyFeedback === "ok") return "Copied!";
    return copyFeedback === "failed" ? "Copy failed" : "Copy image";
  }

  async function ensureBlob(): Promise<Blob> {
    const capture = captureScreenshot;
    if (!capture) throw new Error("No screenshot source");
    if (blobCache) return blobCache;
    if (!capturePending) {
      const seq = captureSeq;
      capturePending = (async () => {
        try {
          const blob = await capture();
          if (seq === captureSeq) blobCache = blob;
          return blob;
        } finally {
          // Clear the shared promise either way: a failed capture must not be
          // handed to the next caller, which should retry.
          if (seq === captureSeq) capturePending = null;
        }
      })();
    }
    return capturePending;
  }

  // Start each open from a clean slate: default to Markdown and drop the cached
  // screenshot so a re-open re-renders the current table. This depends only on
  // `open` -- writing the state variables below does not create a dependency on
  // them, so a later tab switch or render does not re-trigger this reset. It is
  // declared before the capture effect below so a re-open resets the tab before
  // that effect can start a capture for the previous session's tab.
  $effect(() => {
    if (open) {
      tab = "markdown";
      captured = false;
      blobCache = null;
      capturePending = null;
      captureSeq++;
      imageUrl = null;
      preview = "idle";
      actionBusy = false;
      downloadFeedback = "idle";
      copyFeedback = "idle";
      copyHint = false;
    }
  });

  // Publish a captured blob to the preview pane. Used by the JIT render and by
  // a Download/Copy click that had to retry the capture, so the pane stops
  // claiming the render failed once one has succeeded.
  function showCapture(blob: Blob) {
    captured = true;
    preview = "ready";
    if (imageUrl) URL.revokeObjectURL(imageUrl);
    imageUrl = URL.createObjectURL(blob);
  }

  // JIT: render the screenshot the first time the Screenshot tab is shown while
  // the dialog is open; reuse the cached blob for subsequent actions.
  $effect(() => {
    if (!(open && tab === "screenshot" && captureScreenshot && !captured)) return;
    let cancelled = false;
    preview = "busy";
    ensureBlob()
      .then((blob) => {
        if (!cancelled) showCapture(blob);
      })
      .catch((err) => {
        console.error("Failed to render screenshot:", err);
        if (!cancelled) preview = "failed";
      });
    return () => {
      cancelled = true;
    };
  });

  // A re-open while this action is in flight bumps captureSeq; the result of a
  // stale click is dropped rather than downloaded or copied.
  async function copyImage() {
    if (!captureScreenshot || busy) return;
    const seq = captureSeq;
    actionBusy = true;
    try {
      const blob = await ensureBlob();
      if (seq !== captureSeq) return;
      showCapture(blob);
      const copied = await copyPngToClipboard(blob);
      if (copied) {
        copyHint = false;
        flashFeedback("copy", "ok");
      } else {
        // Programmatic copy needs a secure context. Fall back to the preview:
        // right-click the image to copy or save it.
        copyHint = true;
        flashFeedback("copy", "failed");
      }
    } catch (err) {
      console.error("Failed to copy image:", err);
      if (seq === captureSeq) {
        copyHint = true;
        preview = "failed";
        flashFeedback("copy", "failed");
      }
    } finally {
      if (seq === captureSeq) actionBusy = false;
    }
  }

  async function downloadImage() {
    if (!captureScreenshot || busy) return;
    const seq = captureSeq;
    actionBusy = true;
    try {
      const blob = await ensureBlob();
      if (seq !== captureSeq) return;
      showCapture(blob);
      downloadPngBlob(blob, `${fileName}.png`);
      flashFeedback("download", "ok");
    } catch (err) {
      console.error("Failed to download image:", err);
      if (seq === captureSeq) {
        preview = "failed";
        flashFeedback("download", "failed");
      }
    } finally {
      if (seq === captureSeq) actionBusy = false;
    }
  }

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
      for (const timer of Object.values(feedbackTimers)) {
        if (timer !== undefined) clearTimeout(timer);
      }
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
          {:else if preview === "failed"}
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
        <Button
          variant="outline"
          onclick={downloadImage}
          disabled={!captureScreenshot || busy}
        >
          {actionLabel("download")}
        </Button>
        <Button variant="outline" onclick={copyImage} disabled={!captureScreenshot || busy}>
          {actionLabel("copy")}
        </Button>
      {/if}
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
