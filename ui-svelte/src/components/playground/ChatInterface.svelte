<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion, type Endpoint } from "../../lib/chatApi";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import type { ChatMessage, ContentPart } from "../../lib/types";
  import ChatMessageComponent from "./ChatMessage.svelte";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import { Settings, Paperclip } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import * as Select from "$lib/components/ui/select/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import { X } from "@lucide/svelte";

  const selectedModelStore = persistentStore<string>("playground-selected-model", "");
  const systemPromptStore = persistentStore<string>("playground-system-prompt", "");
  const temperatureStore = persistentStore<number>("playground-temperature", 0.7);
  const endpointStore = persistentStore<Endpoint>("playground-endpoint", "v1/chat/completions");
  const maxTokensStore = persistentStore<number>("playground-max-tokens", 4096);

  function loadMessages(): ChatMessage[] {
    try {
      const saved = localStorage.getItem("playground-messages");
      return saved ? JSON.parse(saved) : [];
    } catch {
      return [];
    }
  }

  let messages = $state<ChatMessage[]>(loadMessages());
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

  let hasModels = $derived($models.some((m) => !m.unlisted));
  let userScrolledUp = $state(false);

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

  async function regenerateFromIndex(idx: number) {
    // Remove all messages after the edited user message
    messages = messages.slice(0, idx + 1);

    // Add empty assistant message for the new response
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    isReasoning = false;
    reasoningStartTime = 0;
    abortController = new AbortController();

    try {
      // Build messages array with optional system prompt
      const apiMessages: ChatMessage[] = [];
      if ($systemPromptStore.trim()) {
        apiMessages.push({ role: "system", content: $systemPromptStore.trim() });
      }
      apiMessages.push(...messages.slice(0, -1)); // Add all messages except the empty assistant one

      const stream = streamChatCompletion(
        $selectedModelStore,
        apiMessages,
        abortController.signal,
        { temperature: $temperatureStore, endpoint: $endpointStore, max_tokens: $maxTokensStore }
      );

      for await (const chunk of stream) {
        if (chunk.done) break;

        // Handle reasoning content
        if (chunk.reasoning_content) {
          // Start timing on first reasoning content
          if (!isReasoning) {
            isReasoning = true;
            reasoningStartTime = Date.now();
          }

          // Update the last message with reasoning content
          messages = messages.map((msg, i) =>
            i === messages.length - 1
              ? { ...msg, reasoning_content: (msg.reasoning_content || "") + chunk.reasoning_content }
              : msg
          );
        }

        // Handle regular content - end reasoning phase when we get content
        if (chunk.content) {
          if (isReasoning) {
            // Calculate reasoning time
            const reasoningTimeMs = Date.now() - reasoningStartTime;
            isReasoning = false;

            // Update message with reasoning time
            messages = messages.map((msg, i) =>
              i === messages.length - 1
                ? { ...msg, reasoningTimeMs }
                : msg
            );
          }

          // Update the last message (assistant) with new content
          messages = messages.map((msg, i) =>
            i === messages.length - 1
              ? { ...msg, content: msg.content + chunk.content }
              : msg
          );
        }
      }
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") {
        // User cancelled, keep partial response
        // If we were still reasoning, record the time
        if (isReasoning && reasoningStartTime > 0) {
          const reasoningTimeMs = Date.now() - reasoningStartTime;
          messages = messages.map((msg, i) =>
            i === messages.length - 1
              ? { ...msg, reasoningTimeMs }
              : msg
          );
        }
      } else {
        // Show error in the assistant message
        const errorMessage = error instanceof Error ? error.message : "An error occurred";
        messages = messages.map((msg, i) =>
          i === messages.length - 1
            ? { ...msg, content: msg.content + `\n\n**Error:** ${errorMessage}` }
            : msg
        );
      }
    } finally {
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
    if (event.key === "Enter" && !event.shiftKey) {
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
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <ModelSelector bind:value={$selectedModelStore} placeholder="Select a model..." disabled={isStreaming} />
    <div class="flex gap-2">
      <Button variant="outline" size="icon" onclick={() => (showSettings = true)} title="Settings">
        <Settings />
      </Button>
      <Button variant="outline" onclick={newChat} disabled={messages.length === 0 && !isStreaming}>
        New Chat
      </Button>
    </div>
  </div>

  <!-- Settings dialog -->
  <Dialog.Root bind:open={showSettings}>
    <Dialog.Content class="max-w-xl">
      <Dialog.Header>
        <Dialog.Title>Chat Settings</Dialog.Title>
      </Dialog.Header>

      <div class="space-y-4">
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
          <Label class="mb-1" for="system-prompt">System Prompt</Label>
          <Textarea
            id="system-prompt"
            class="resize-none"
            placeholder="You are a helpful assistant..."
            rows={3}
            bind:value={$systemPromptStore}
            disabled={isStreaming}
          />
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
          <Label class="mb-1" for="max-tokens">Max Tokens</Label>
          <Input id="max-tokens" type="number" min="1" bind:value={$maxTokensStore} disabled={isStreaming} />
          <p class="text-muted-foreground mt-1 text-xs">Required for /v1/messages.</p>
        </div>
      </div>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => (showSettings = false)}>Done</Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>

  <!-- Empty state for no models configured -->
  {#if !hasModels}
    <div class="text-muted-foreground flex flex-1 items-center justify-center">
      <p>No models configured. Add models to your configuration to start chatting.</p>
    </div>
  {:else}
    <!-- Messages area -->
    <div
      class="mb-4 flex-1 overflow-y-auto px-2"
      bind:this={messagesContainer}
      onscroll={handleMessagesScroll}
    >
      {#if messages.length === 0}
        <div class="text-muted-foreground flex h-full items-center justify-center">
          <p>Start a conversation by typing a message below.</p>
        </div>
      {:else}
        {#each messages as message, idx (idx)}
          <ChatMessageComponent
            role={message.role}
            content={message.content}
            reasoning_content={message.reasoning_content}
            reasoningTimeMs={message.reasoningTimeMs}
            isStreaming={isStreaming && idx === messages.length - 1 && message.role === "assistant"}
            isReasoning={isReasoning && idx === messages.length - 1 && message.role === "assistant"}
            onEdit={message.role === "user" ? (newContent) => editMessage(idx, newContent) : undefined}
            onRegenerate={message.role === "assistant" && idx > 0 && messages[idx - 1].role === "user"
              ? () => regenerateFromIndex(idx - 1)
              : undefined}
          />
        {/each}
      {/if}
    </div>

    <!-- Input area -->
    <div class="shrink-0">
      <!-- Image preview strip -->
      {#if attachedImages.length > 0}
        <div class="mb-2 flex flex-wrap gap-2">
          {#each attachedImages as imageUrl, idx (idx)}
            <div class="group relative">
              <img
                src={imageUrl}
                alt="Attached image {idx + 1}"
                class="h-20 w-20 rounded-md border object-cover"
              />
              <Button
                variant="destructive"
                size="icon-sm"
                class="absolute -right-2 -top-2 h-6 w-6 rounded-full opacity-0 transition-opacity group-hover:opacity-100"
                onclick={() => removeImage(idx)}
                title="Remove image"
              >
                <X class="size-3" />
              </Button>
            </div>
          {/each}
        </div>
      {/if}

      <!-- Error message -->
      {#if imageError}
        <div class="bg-destructive/10 text-destructive mb-2 rounded-md p-2 text-sm">
          {imageError}
        </div>
      {/if}

      <div class="flex gap-2">
        <!-- Hidden file input -->
        <input
          type="file"
          accept=".jpg,.jpeg,.png,.gif,.webp"
          multiple
          class="hidden"
          bind:this={fileInput}
          onchange={handleImageSelect}
        />

        <ExpandableTextarea
          bind:ref={inputRef}
          bind:value={userInput}
          placeholder="Type a message..."
          rows={3}
          onkeydown={handleKeyDown}
          disabled={isStreaming || !$selectedModelStore}
        />
        <div class="flex flex-col gap-2">
          {#if isStreaming}
            <Button variant="destructive" onclick={cancelStreaming}>Cancel</Button>
          {:else}
            <Button
              variant="outline"
              size="icon"
              onclick={() => fileInput?.click()}
              disabled={isStreaming || !$selectedModelStore}
              title="Attach image"
            >
              <Paperclip />
            </Button>
            <Button
              onclick={sendMessage}
              disabled={(!userInput.trim() && attachedImages.length === 0) || !$selectedModelStore}
            >
              Send
            </Button>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
