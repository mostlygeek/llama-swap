<script lang="ts">
  import { get } from "svelte/store";
  import { hasListedModels, playgroundModels } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { showGenerationStats } from "../../stores/generationStats";
  import { streamChatCompletion, type Endpoint } from "../../lib/chatApi";
  import { currentStats, markCancelled, startTracking, trackChunk } from "../../lib/generationStats";
  import { DOCS_AGENT_SYSTEM_PROMPT } from "../../lib/prompts/docsAgent";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import type { ChatMessage, ContentPart } from "../../lib/types";
  import { isSubmitEnter } from "../../lib/ime";
  import ChatMessageComponent from "./ChatMessage.svelte";
  import ModelSelector from "./ModelSelector.svelte";
  import ChatComposer from "./ChatComposer.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { Settings, Paperclip, SquarePen } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import { Tabs, TabsContent, TabsList, TabsTrigger } from "$lib/components/ui/tabs/index.js";
  import { X } from "@lucide/svelte";

  const selectedModelStore = persistentStore<string>("playground-selected-model", "");
  const systemPromptStore = persistentStore<string>("playground-system-prompt", "");
  const temperatureStore = persistentStore<number>("playground-temperature", 0.7);
  const endpointStore = persistentStore<Endpoint>("playground-endpoint", "v1/chat/completions");
  const maxTokensStore = persistentStore<number>("playground-max-tokens", 4096);
  const topPStore = persistentStore<number>("playground-top-p", 1);
  const topKStore = persistentStore<number>("playground-top-k", 40);
  const minPStore = persistentStore<number>("playground-min-p", 0);

  // This tab was briefly the docs agent; that is the Docs tab now. Anyone who
  // used it in between has the agent's prompt persisted here, which is not a
  // prompt they chose, so it is cleared once.
  if (get(systemPromptStore) === DOCS_AGENT_SYSTEM_PROMPT) {
    systemPromptStore.set("");
  }

  /** Chat holds no tool turns; the Docs tab is where tool calling lives. */
  type PlainMessage = ChatMessage & { role: "user" | "assistant" | "system" };

  function loadMessages(): PlainMessage[] {
    try {
      const saved = localStorage.getItem("playground-messages");
      const parsed = saved ? JSON.parse(saved) : [];
      if (!Array.isArray(parsed)) return [];
      // A history left over from when this tab was agentic still holds tool
      // turns this tab cannot render or replay. Sending one upstream is a hard
      // 400, so they are dropped on load.
      return parsed.filter(
        (msg: ChatMessage): msg is PlainMessage => msg?.role !== "tool" && !msg?.tool_calls?.length
      );
    } catch {
      return [];
    }
  }

  let messages = $state<PlainMessage[]>(loadMessages());
  let userInput = $state("");
  let isStreaming = $state(false);
  let isReasoning = $state(false);
  let reasoningStartTime = $state<number>(0);
  let abortController = $state<AbortController | null>(null);
  let messagesContainer: HTMLDivElement | undefined = $state();
  let inputRef: HTMLTextAreaElement | null = $state(null);
  let showSettings = $state(false);
  let attachedImages = $state<string[]>([]);
  let fileInput = $state<HTMLInputElement | null>(null);
  let imageError = $state<string | null>(null);

  let userScrolledUp = $state(false);

  /**
   * Context window of the model the next request goes to. Looked up when the
   * turn starts and stored with its stats, so a later switch of the dropdown
   * does not rewrite older turns. The selector also offers aliases, which are
   * not model entries themselves, so those resolve through their model.
   */
  function selectedContextLength(): number | undefined {
    const id = $selectedModelStore;
    return $playgroundModels.find((m) => m.id === id || m.aliases?.includes(id))?.context_length;
  }

  $effect(() => {
    playgroundStores.chatStreaming.set(isStreaming);
  });

  let wasStreaming = $state(false);
  $effect(() => {
    if (wasStreaming && !isStreaming) {
      inputRef?.focus();
    }
    wasStreaming = isStreaming;
  });

  // The settings dialog follows the visual viewport (see TextEditDialog):
  // mobile browsers keep the layout viewport full size when the keyboard
  // opens, so without this the keys cover the bottom of the dialog. While
  // open, the dialog tracks the visible area and is anchored to its top.
  let visibleViewport = $state<{ top: number; height: number } | null>(null);
  $effect(() => {
    if (!showSettings) return;
    const vv = window.visualViewport;
    if (!vv) return;
    const update = () => {
      visibleViewport = { top: vv.offsetTop, height: vv.height };
    };
    update();
    vv.addEventListener("resize", update);
    vv.addEventListener("scroll", update);
    return () => {
      vv.removeEventListener("resize", update);
      vv.removeEventListener("scroll", update);
    };
  });

  const settingsStyle = $derived(
    visibleViewport
      ? `top: ${visibleViewport.top + 16}px; max-height: ${visibleViewport.height - 32}px`
      : ""
  );

  // The prompt is bound straight to the store, so "save" is the dialog
  // closing (Done, X, overlay, Escape). That is when surrounding whitespace
  // is trimmed, whichever way it closes.
  $effect(() => {
    if (showSettings) return;
    const trimmed = $systemPromptStore.trim();
    if (trimmed !== $systemPromptStore) systemPromptStore.set(trimmed);
  });

  function handleMessagesScroll() {
    if (!messagesContainer) return;
    const { scrollTop, scrollHeight, clientHeight } = messagesContainer;
    // Consider "at bottom" if within 40px of the bottom
    userScrolledUp = scrollHeight - scrollTop - clientHeight > 40;
  }

  // Auto-scroll when messages change — skip if user scrolled up
  $effect(() => {
    if (messages.length > 0 && messagesContainer && !userScrolledUp) {
      messagesContainer.scrollTo({
        top: messagesContainer.scrollHeight,
        behavior: isStreaming ? "instant" : "smooth",
      });
    }
  });

  // Persist messages to localStorage (throttled to once per 2s)
  let lastSaveTime = 0;
  $effect(() => {
    const json = JSON.stringify(messages);
    const elapsed = Date.now() - lastSaveTime;
    const save = () => {
      try { localStorage.setItem("playground-messages", json); } catch {}
      lastSaveTime = Date.now();
    };
    if (elapsed >= 2000) {
      save();
      return;
    }
    const timer = setTimeout(save, 2000 - elapsed);
    return () => clearTimeout(timer);
  });

  async function sendMessage() {
    const trimmedInput = userInput.trim();
    if ((!trimmedInput && attachedImages.length === 0) || !$selectedModelStore || isStreaming) return;

    userScrolledUp = false;

    // Build message content (multimodal if images attached)
    let content: string | ContentPart[];
    if (attachedImages.length > 0) {
      const parts: ContentPart[] = [];
      if (trimmedInput) {
        parts.push({ type: "text", text: trimmedInput });
      }
      for (const url of attachedImages) {
        parts.push({ type: "image_url", image_url: { url } });
      }
      content = parts;
    } else {
      content = trimmedInput;
    }

    // Add user message
    messages = [...messages, { role: "user", content }];
    userInput = "";
    attachedImages = [];
    imageError = null;

    // Generate response from the new user message
    await regenerateFromIndex(messages.length - 1);
  }

  function cancelStreaming() {
    abortController?.abort();
  }

  function newChat() {
    if (isStreaming) {
      cancelStreaming();
    }
    messages = [];
    isReasoning = false;
    reasoningStartTime = 0;
  }

  /** Merges a patch into the last message. */
  function patchLast(patch: Partial<PlainMessage>) {
    messages = messages.map((msg, i) => (i === messages.length - 1 ? { ...msg, ...patch } : msg));
  }

  function lastText(): string {
    const last = messages[messages.length - 1];
    return typeof last?.content === "string" ? last.content : "";
  }

  /** Applies a streamed delta to the in-progress assistant message. */
  function appendDelta(kind: "content" | "reasoning", text: string) {
    if (kind === "reasoning") {
      if (!isReasoning) {
        isReasoning = true;
        reasoningStartTime = Date.now();
      }
      const last = messages[messages.length - 1];
      patchLast({ reasoning_content: (last?.reasoning_content || "") + text });
      return;
    }

    // The first content delta ends the reasoning phase.
    if (isReasoning) {
      patchLast({ reasoningTimeMs: Date.now() - reasoningStartTime });
      isReasoning = false;
    }
    patchLast({ content: lastText() + text });
  }

  function handleTurnError(error: unknown) {
    if (error instanceof Error && error.name === "AbortError") {
      // Cancelled by the user: keep the partial response.
      if (isReasoning && reasoningStartTime > 0) {
        patchLast({ reasoningTimeMs: Date.now() - reasoningStartTime });
      }
      return;
    }
    const message = error instanceof Error ? error.message : "An error occurred";
    patchLast({ content: lastText() + `\n\n**Error:** ${message}` });
  }

  /** The conversation to send, with the optional system prompt prepended. */
  function requestMessages(): ChatMessage[] {
    const out: ChatMessage[] = [];
    if ($systemPromptStore.trim()) {
      out.push({ role: "system", content: $systemPromptStore.trim() });
    }
    out.push(...messages.slice(0, -1)); // everything but the empty placeholder
    return out;
  }

  /**
   * The stats a message is shown with. An assistant turn carries its own; a
   * user message borrows the turn it prompted, so its prompt-processing line
   * sits right under it. Nothing when stats are switched off in settings;
   * they are still tracked, so switching them back on shows past turns too.
   */
  function statsFor(idx: number) {
    if (!$showGenerationStats) return undefined;
    const msg = messages[idx];
    if (msg.role === "assistant") return msg.stats;
    const next = messages[idx + 1];
    return next?.role === "assistant" ? next.stats : undefined;
  }

  /** True for the streaming assistant turn and the user message that prompted it. */
  function statsLiveFor(idx: number) {
    return $showGenerationStats && isStreaming && idx >= messages.length - 2;
  }

  async function regenerateFromIndex(idx: number) {
    // Remove all messages after the edited user message
    messages = messages.slice(0, idx + 1);

    // Add empty assistant message for the new response
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    isReasoning = false;
    reasoningStartTime = 0;
    abortController = new AbortController();

    // Stats are refreshed on every chunk and, so the clocks keep moving while
    // the backend is quiet (prompt processing, a slow token), on a timer too.
    const tracker = startTracking(performance.now(), selectedContextLength());
    const ticker = window.setInterval(() => {
      patchLast({ stats: currentStats(tracker, performance.now(), true) });
    }, 200);

    try {
      const stream = streamChatCompletion(
        $selectedModelStore,
        requestMessages(),
        abortController.signal,
        {
          temperature: $temperatureStore,
          endpoint: $endpointStore,
          max_tokens: $maxTokensStore,
          top_p: $topPStore,
          top_k: $topKStore,
          min_p: $minPStore,
        }
      );

      for await (const chunk of stream) {
        const now = performance.now();
        trackChunk(tracker, chunk, now);
        if (chunk.done) break;
        if (chunk.reasoning_content) appendDelta("reasoning", chunk.reasoning_content);
        if (chunk.content) appendDelta("content", chunk.content);
        patchLast({ stats: currentStats(tracker, now, true) });
      }
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") markCancelled(tracker);
      handleTurnError(error);
    } finally {
      window.clearInterval(ticker);
      patchLast({ stats: currentStats(tracker, performance.now(), false) });
      isStreaming = false;
      isReasoning = false;
      abortController = null;
    }
  }

  async function editMessage(idx: number, newContent: string) {
    if (isStreaming || !$selectedModelStore) return;

    // Update the user message at the specified index
    messages = messages.map((msg, i) =>
      i === idx ? { ...msg, content: newContent } : msg
    );

    // Trigger a new chat request with the updated messages
    await regenerateFromIndex(idx);
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (isSubmitEnter(event)) {
      event.preventDefault();
      sendMessage();
    }
  }

  const ACCEPTED_IMAGE_FORMATS = ["image/jpeg", "image/png", "image/gif", "image/webp"];
  const MAX_IMAGE_SIZE = 20 * 1024 * 1024; // 20MB
  const MAX_IMAGES_PER_MESSAGE = 5;

  function validateImageFile(file: File): string | null {
    if (!ACCEPTED_IMAGE_FORMATS.includes(file.type)) {
      return `Invalid file type: ${file.type}. Accepted formats: JPG, PNG, GIF, WEBP`;
    }
    if (file.size > MAX_IMAGE_SIZE) {
      return `File too large: ${(file.size / 1024 / 1024).toFixed(1)}MB. Maximum size: 20MB`;
    }
    return null;
  }

  function fileToDataUrl(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = () => reject(new Error("Failed to read file"));
      reader.readAsDataURL(file);
    });
  }

  async function processImageFiles(files: File[]): Promise<void> {
    imageError = null;

    if (attachedImages.length + files.length > MAX_IMAGES_PER_MESSAGE) {
      imageError = `Maximum ${MAX_IMAGES_PER_MESSAGE} images per message`;
      return;
    }

    for (const file of files) {
      const error = validateImageFile(file);
      if (error) {
        imageError = error;
        return;
      }
    }

    try {
      const dataUrls = await Promise.all(files.map(fileToDataUrl));
      attachedImages = [...attachedImages, ...dataUrls];
    } catch (error) {
      imageError = error instanceof Error ? error.message : "Failed to process images";
    }
  }

  function handleImageSelect(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      processImageFiles(Array.from(input.files));
    }
    // Reset the input so the same file can be selected again
    input.value = "";
  }

  function removeImage(idx: number) {
    attachedImages = attachedImages.filter((_, i) => i !== idx);
    imageError = null;
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model selector and controls -->
  <div class="mb-3 flex shrink-0 gap-2">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select a model..." disabled={isStreaming} />
    <div class="flex shrink-0 gap-2">
      <Button variant="outline" size="icon" onclick={() => (showSettings = true)} title="Chat settings">
        <Settings />
      </Button>
      <Button variant="outline" onclick={newChat} disabled={messages.length === 0 && !isStreaming} title="New chat">
        <SquarePen />
        <span class="hidden sm:inline">New Chat</span>
      </Button>
    </div>
  </div>

  <!-- Settings dialog -->
  <Dialog.Root bind:open={showSettings}>
    <Dialog.Content
      class="top-8 translate-y-0 max-w-xl max-h-[calc(100dvh-2rem)] overflow-y-auto"
      style={settingsStyle}
    >
      <Dialog.Header>
        <Dialog.Title>Chat Settings</Dialog.Title>
      </Dialog.Header>

      <!-- Stacking every setting in one scroll left the system prompt textarea
         huge on a phone with no way to shrink the window, so the sections are
         tabs now and only one shows at a time. -->
      <Tabs value="prompt" class="flex flex-col gap-2">
        <TabsList variant="line" class="justify-start">
          <TabsTrigger value="prompt">Prompt</TabsTrigger>
          <TabsTrigger value="params">Parameters</TabsTrigger>
        </TabsList>

        <TabsContent value="prompt">
          <Label class="sr-only" for="system-prompt">System Prompt</Label>
          <Textarea
            id="system-prompt"
            class="resize-y min-h-24 max-h-[50dvh]"
            placeholder="You are a helpful assistant..."
            rows={5}
            bind:value={$systemPromptStore}
            disabled={isStreaming}
          />
        </TabsContent>

        <TabsContent value="params" class="space-y-4">
          <div>
            <Label class="mb-1" for="endpoint">Endpoint</Label>
            <Select.Root
              type="single"
              value={$endpointStore}
              onValueChange={(v) => v && endpointStore.set(v as Endpoint)}
            >
              <Select.Trigger class="w-full">/{$endpointStore}</Select.Trigger>
              <Select.Content>
                <Select.Item value="v1/chat/completions">/v1/chat/completions</Select.Item>
                <Select.Item value="v1/messages">/v1/messages</Select.Item>
                <Select.Item value="v1/responses">/v1/responses</Select.Item>
              </Select.Content>
            </Select.Root>
          </div>
          <div>
            <Label class="mb-1" for="temperature">
              Temperature: {$temperatureStore.toFixed(2)}
            </Label>
            <input
              id="temperature"
              type="range"
              min="0"
              max="2"
              step="0.05"
              class="accent-primary w-full"
              bind:value={$temperatureStore}
              disabled={isStreaming}
            />
            <div class="text-muted-foreground mt-1 flex justify-between text-xs">
              <span>Precise (0)</span>
              <span>Creative (2)</span>
            </div>
          </div>
          <div>
            <Label class="mb-1" for="top-p">
              Top P (nucleus): {$topPStore.toFixed(2)}
            </Label>
            <input
              id="top-p"
              type="range"
              min="0.01"
              max="1"
              step="0.01"
              class="accent-primary w-full"
              bind:value={$topPStore}
              disabled={isStreaming}
            />
            <div class="text-muted-foreground mt-1 flex justify-between text-xs">
              <span>Narrow (0.01)</span>
              <span>Off (1)</span>
            </div>
          </div>
          <div>
            <Label class="mb-1" for="min-p">
              Min P: {$minPStore.toFixed(2)}
            </Label>
            <input
              id="min-p"
              type="range"
              min="0"
              max="1"
              step="0.01"
              class="accent-primary w-full"
              bind:value={$minPStore}
              disabled={isStreaming}
            />
            <p class="text-muted-foreground mt-1 text-xs">Off at 0. Only sent on /v1/chat/completions.</p>
          </div>
          <div>
            <Label class="mb-1" for="top-k">Top K</Label>
            <Input
              id="top-k"
              type="number"
              min="0"
              max="10000"
              step="1"
              bind:value={$topKStore}
              disabled={isStreaming}
            />
            <p class="text-muted-foreground mt-1 text-xs">0 disables it. Not sent on /v1/responses.</p>
          </div>
          <div>
            <Label class="mb-1" for="max-tokens">Max Tokens</Label>
            <Input id="max-tokens" type="number" min="1" bind:value={$maxTokensStore} disabled={isStreaming} />
            <p class="text-muted-foreground mt-1 text-xs">Required for /v1/messages.</p>
          </div>
        </TabsContent>
      </Tabs>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => (showSettings = false)}>Done</Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>

  <!-- Empty state for no models configured -->
  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add models to your configuration to start chatting." />
  {:else}
    <!-- Messages area. The conversation is one centered column, the same
         width as the composer below it, so lines stay readable on a wide
         desktop and take the full width of a phone. -->
    <div
      class="mb-3 flex-1 overflow-y-auto px-1 sm:px-2"
      bind:this={messagesContainer}
      onscroll={handleMessagesScroll}
    >
      {#if messages.length === 0}
        <EmptyState full message="Start a conversation by typing a message below." />
      {:else}
        <div class="mx-auto w-full max-w-3xl py-2">
        {#each messages as message, idx (idx)}
          <ChatMessageComponent
            role={message.role}
            content={message.content}
            reasoning_content={message.reasoning_content}
            reasoningTimeMs={message.reasoningTimeMs}
            isStreaming={isStreaming && idx === messages.length - 1 && message.role === "assistant"}
            isReasoning={isReasoning && idx === messages.length - 1 && message.role === "assistant"}
            stats={statsFor(idx)}
            statsLive={statsLiveFor(idx)}
            onEdit={message.role === "user" ? (newContent) => editMessage(idx, newContent) : undefined}
            onRegenerate={message.role === "assistant" && idx > 0 && messages[idx - 1].role === "user"
              ? () => regenerateFromIndex(idx - 1)
              : undefined}
          />
        {/each}
        </div>
      {/if}
    </div>

    <!-- Input area -->
    <div class="mx-auto w-full max-w-3xl shrink-0 px-1 sm:px-2">
      <!-- Hidden file input -->
      <input
        type="file"
        accept=".jpg,.jpeg,.png,.gif,.webp"
        multiple
        class="hidden"
        bind:this={fileInput}
        onchange={handleImageSelect}
      />

      <ChatComposer
        bind:ref={inputRef}
        bind:value={userInput}
        placeholder="Type a message..."
        onkeydown={handleKeyDown}
        disabled={isStreaming || !$selectedModelStore}
        streaming={isStreaming}
        canSend={Boolean(userInput.trim()) || attachedImages.length > 0}
        onsend={sendMessage}
        onstop={cancelStreaming}
      >
        {#snippet attachments()}
          {#if attachedImages.length > 0}
            <div class="flex flex-wrap gap-2 px-3 pt-3">
              {#each attachedImages as imageUrl, idx (idx)}
                <div class="relative">
                  <img
                    src={imageUrl}
                    alt="Attached image {idx + 1}"
                    class="size-16 rounded-lg object-cover"
                  />
                  <Button
                    variant="secondary"
                    size="icon-xs"
                    class="absolute -top-1.5 -right-1.5 rounded-full shadow-sm"
                    onclick={() => removeImage(idx)}
                    title="Remove image"
                  >
                    <X class="size-3" />
                  </Button>
                </div>
              {/each}
            </div>
          {/if}
          {#if imageError}
            <div class="text-destructive px-3 pt-2 text-xs">
              {imageError}
            </div>
          {/if}
        {/snippet}
        {#snippet leading()}
          <Button
            variant="ghost"
            size="icon-lg"
            class="text-muted-foreground shrink-0 rounded-full"
            onclick={() => fileInput?.click()}
            disabled={isStreaming || !$selectedModelStore}
            title="Attach image"
            aria-label="Attach image"
          >
            <Paperclip />
          </Button>
        {/snippet}
      </ChatComposer>
    </div>
  {/if}
</div>

