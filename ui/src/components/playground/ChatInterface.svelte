<script lang="ts">
  import { untrack } from "svelte";
  import { derived } from "svelte/store";
  import { hasListedModels, playgroundModels } from "../../stores/api";
  import { chatHistory, chatMessages, activeChat } from "../../stores/chatHistory";
  import { showGenerationStats } from "../../stores/generationStats";
  import type { Endpoint } from "../../lib/chatApi";
  import { chatGeneration } from "../../lib/chatGeneration";
  import type { ContentPart } from "../../lib/types";
  import { isSubmitEnter } from "../../lib/ime";
  import { appendImageFiles, pasteImageFiles } from "../../lib/chatImages";
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

  const selectedModelStore = chatHistory.setting("model");
  const systemPromptStore = chatHistory.setting("systemPrompt");
  const temperatureStore = chatHistory.setting("temperature");
  const endpointStore = chatHistory.setting("endpoint");
  const maxTokensStore = chatHistory.setting("maxTokens");
  const topPStore = chatHistory.setting("topP");
  const topKStore = chatHistory.setting("topK");
  const minPStore = chatHistory.setting("minP");

  let userInput = $state("");
  const streaming = derived([activeChat, chatHistory.generating], ([chat, ids]) => ids.has(chat.id));
  const reasoning = chatGeneration.reasoning;
  const isReasoning = $derived(Boolean($reasoning[$activeChat.id]));
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

  let previousChatId = "";
  $effect(() => {
    const id = $activeChat.id;
    if (id === previousChatId) return;
    previousChatId = id;
    untrack(() => {
      userInput = "";
      attachedImages = [];
      imageError = null;
      userScrolledUp = false;
    });
  });

  let wasStreaming = $state(false);
  $effect(() => {
    if (wasStreaming && !$streaming) {
      inputRef?.focus();
    }
    wasStreaming = $streaming;
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
    if ($chatMessages.length > 0 && messagesContainer && !userScrolledUp) {
      messagesContainer.scrollTo({
        top: messagesContainer.scrollHeight,
        behavior: $streaming ? "instant" : "smooth",
      });
    }
  });

  async function sendMessage() {
    const trimmedInput = userInput.trim();
    if ((!trimmedInput && attachedImages.length === 0) || !$selectedModelStore || $streaming) return;

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
    $chatMessages = [...$chatMessages, { role: "user", content }];
    userInput = "";
    attachedImages = [];
    imageError = null;

    // Generate response from the new user message
    await regenerateFromIndex($chatMessages.length - 1);
  }

  function cancelStreaming() {
    chatGeneration.cancel($activeChat.id);
  }

  function newChat() {
    chatHistory.create();
  }

  /**
   * The stats a message is shown with. An assistant turn carries its own; a
   * user message borrows the turn it prompted, so its prompt-processing line
   * sits right under it. Nothing when stats are switched off in settings;
   * they are still tracked, so switching them back on shows past turns too.
   */
  function statsFor(idx: number) {
    if (!$showGenerationStats) return undefined;
    const msg = $chatMessages[idx];
    if (msg.role === "assistant") return msg.stats;
    const next = $chatMessages[idx + 1];
    return next?.role === "assistant" ? next.stats : undefined;
  }

  /** True for the streaming assistant turn and the user message that prompted it. */
  function statsLiveFor(idx: number) {
    return $showGenerationStats && $streaming && idx >= $chatMessages.length - 2;
  }

  async function regenerateFromIndex(idx: number) {
    await chatGeneration.generate($activeChat.id, idx, selectedContextLength());
  }

  async function editMessage(idx: number, newContent: string) {
    if ($streaming || !$selectedModelStore) return;

    // Update the user message at the specified index
    $chatMessages = $chatMessages.map((msg, i) =>
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

  async function processImageFiles(files: File[]): Promise<void> {
    imageError = null;
    const error = await appendImageFiles(files, () => attachedImages, (images) => {
      attachedImages = images;
    });
    if (error) imageError = error;
  }

  function handleImageSelect(event: Event) {
    const input = event.target as HTMLInputElement;
    if (input.files && input.files.length > 0) {
      processImageFiles(Array.from(input.files));
    }
    // Reset the input so the same file can be selected again
    input.value = "";
  }

  function handleImagePaste(event: ClipboardEvent) {
    pasteImageFiles(event, processImageFiles);
  }

  function removeImage(idx: number) {
    attachedImages = attachedImages.filter((_, i) => i !== idx);
    imageError = null;
  }
</script>

<div class="flex flex-col h-full">
  <!-- Model selector and controls -->
  <div class="mb-3 flex shrink-0 gap-2">
    <ModelSelector
      bind:value={$selectedModelStore}
      placeholder="Select a model..."
      disabled={$streaming}
      match={{ inputModalities: ["text", "image"], outputModalities: ["text"] }}
    />
    <div class="flex shrink-0 gap-2">
      <Button variant="outline" size="icon" onclick={() => (showSettings = true)} title="Chat settings">
        <Settings />
      </Button>
      <Button variant="outline" onclick={newChat} disabled={$chatMessages.length === 0} title="New chat">
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
            disabled={$streaming}
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
              disabled={$streaming}
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
              disabled={$streaming}
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
              disabled={$streaming}
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
              disabled={$streaming}
            />
            <p class="text-muted-foreground mt-1 text-xs">0 disables it. Not sent on /v1/responses.</p>
          </div>
          <div>
            <Label class="mb-1" for="max-tokens">Max Tokens</Label>
            <Input id="max-tokens" type="number" min="1" bind:value={$maxTokensStore} disabled={$streaming} />
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
      {#if $chatMessages.length === 0}
        <EmptyState full message="Start a conversation by typing a message below." />
      {:else}
        <div class="mx-auto w-full max-w-3xl py-2">
        {#key $activeChat.id}
        {#each $chatMessages as message, idx (idx)}
          <ChatMessageComponent
            role={message.role}
            content={message.content}
            reasoning_content={message.reasoning_content}
            reasoningTimeMs={message.reasoningTimeMs}
            isStreaming={$streaming && idx === $chatMessages.length - 1 && message.role === "assistant"}
            isReasoning={isReasoning && idx === $chatMessages.length - 1 && message.role === "assistant"}
            stats={statsFor(idx)}
            statsLive={statsLiveFor(idx)}
            onEdit={message.role === "user" ? (newContent) => editMessage(idx, newContent) : undefined}
            interrupted={message.interrupted}
            onRegenerate={!$streaming && message.role === "assistant" && idx > 0 && $chatMessages[idx - 1].role === "user"
              ? () => regenerateFromIndex(idx - 1)
              : undefined}
          />
        {/each}
        {/key}
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
        onpaste={handleImagePaste}
        disabled={$streaming || !$selectedModelStore}
        streaming={$streaming}
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
            disabled={$streaming || !$selectedModelStore}
            title="Attach image or paste it into the message box"
            aria-label="Attach image"
          >
            <Paperclip />
          </Button>
        {/snippet}
      </ChatComposer>
    </div>
  {/if}
</div>

