<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion } from "../../lib/chatApi";
  import type { ChatMessage } from "../../lib/types";
  import ChatMessageComponent from "./ChatMessage.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";

  const selectedModelStore = persistentStore<string>("playground-selected-model", "");
  const systemPromptStore = persistentStore<string>("playground-system-prompt", "");
  const temperatureStore = persistentStore<number>("playground-temperature", 0.7);

  let messages = $state<ChatMessage[]>([]);
  let userInput = $state("");
  let isStreaming = $state(false);
  let isReasoning = $state(false);
  let reasoningStartTime = $state<number>(0);
  let abortController = $state<AbortController | null>(null);
  let messagesContainer: HTMLDivElement | undefined = $state();
  let showSettings = $state(false);

  // Show all models (excluding unlisted), backend will auto-load as needed
  let availableModels = $derived($models.filter((m) => !m.unlisted));

  // Group models into local and peer models by provider
  let groupedModels = $derived.by(() => {
    const local = availableModels.filter((m) => !m.peerID);
    const peerModels = availableModels.filter((m) => m.peerID);

    // Group peer models by peerID
    const peersByProvider = peerModels.reduce(
      (acc, model) => {
        const peerId = model.peerID || "unknown";
        if (!acc[peerId]) acc[peerId] = [];
        acc[peerId].push(model);
        return acc;
      },
      {} as Record<string, typeof availableModels>
    );

    return { local, peersByProvider };
  });

  // Auto-scroll when messages change
  $effect(() => {
    if (messages.length > 0 && messagesContainer) {
      messagesContainer.scrollTo({
        top: messagesContainer.scrollHeight,
        behavior: "smooth",
      });
    }
  });

  async function sendMessage() {
    const trimmedInput = userInput.trim();
    if (!trimmedInput || !$selectedModelStore || isStreaming) return;

    // Add user message
    messages = [...messages, { role: "user", content: trimmedInput }];
    userInput = "";

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
        { temperature: $temperatureStore }
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
</script>

<div class="flex flex-col h-full">
  <!-- Model selector and controls -->
  <div class="shrink-0 flex flex-wrap gap-2 mb-4">
    <select
      class="min-w-0 flex-1 basis-48 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedModelStore}
      disabled={isStreaming}
    >
      <option value="">Select a model...</option>
      {#if groupedModels.local.length > 0}
        <optgroup label="Local">
          {#each groupedModels.local as model (model.id)}
            <option value={model.id}>{model.id}</option>
          {/each}
        </optgroup>
      {/if}
      {#each Object.entries(groupedModels.peersByProvider).sort(([a], [b]) => a.localeCompare(b)) as [peerId, peerModels] (peerId)}
        <optgroup label="Peer: {peerId}">
          {#each peerModels as model (model.id)}
            <option value={model.id}>{model.id}</option>
          {/each}
        </optgroup>
      {/each}
    </select>
    <div class="flex gap-2">
      <button
        class="btn"
        onclick={() => (showSettings = !showSettings)}
        title="Settings"
      >
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" class="w-5 h-5">
          <path fill-rule="evenodd" d="M8.34 1.804A1 1 0 0 1 9.32 1h1.36a1 1 0 0 1 .98.804l.295 1.473c.497.144.971.342 1.416.587l1.25-.834a1 1 0 0 1 1.262.125l.962.962a1 1 0 0 1 .125 1.262l-.834 1.25c.245.445.443.919.587 1.416l1.473.295a1 1 0 0 1 .804.98v1.36a1 1 0 0 1-.804.98l-1.473.295a6.95 6.95 0 0 1-.587 1.416l.834 1.25a1 1 0 0 1-.125 1.262l-.962.962a1 1 0 0 1-1.262.125l-1.25-.834a6.953 6.953 0 0 1-1.416.587l-.295 1.473a1 1 0 0 1-.98.804H9.32a1 1 0 0 1-.98-.804l-.295-1.473a6.957 6.957 0 0 1-1.416-.587l-1.25.834a1 1 0 0 1-1.262-.125l-.962-.962a1 1 0 0 1-.125-1.262l.834-1.25a6.957 6.957 0 0 1-.587-1.416l-1.473-.295A1 1 0 0 1 1 10.68V9.32a1 1 0 0 1 .804-.98l1.473-.295c.144-.497.342-.971.587-1.416l-.834-1.25a1 1 0 0 1 .125-1.262l.962-.962A1 1 0 0 1 5.38 3.03l1.25.834a6.957 6.957 0 0 1 1.416-.587l.294-1.473ZM13 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z" clip-rule="evenodd" />
        </svg>
      </button>
      <button class="btn" onclick={newChat} disabled={messages.length === 0 && !isStreaming}>
        New Chat
      </button>
    </div>
  </div>

  <!-- Settings panel -->
  {#if showSettings}
    <div class="shrink-0 mb-4 p-4 bg-surface border border-gray-200 dark:border-white/10 rounded">
      <div class="mb-4">
        <label class="block text-sm font-medium mb-1" for="system-prompt">System Prompt</label>
        <textarea
          id="system-prompt"
          class="w-full px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-card focus:outline-none focus:ring-2 focus:ring-primary resize-none"
          placeholder="You are a helpful assistant..."
          rows="3"
          bind:value={$systemPromptStore}
          disabled={isStreaming}
        ></textarea>
      </div>
      <div>
        <label class="block text-sm font-medium mb-1" for="temperature">
          Temperature: {$temperatureStore.toFixed(2)}
        </label>
        <input
          id="temperature"
          type="range"
          min="0"
          max="2"
          step="0.05"
          class="w-full"
          bind:value={$temperatureStore}
          disabled={isStreaming}
        />
        <div class="flex justify-between text-xs text-txtsecondary mt-1">
          <span>Precise (0)</span>
          <span>Creative (2)</span>
        </div>
      </div>
    </div>
  {/if}

  <!-- Empty state for no models configured -->
  {#if availableModels.length === 0}
    <div class="flex-1 flex items-center justify-center text-txtsecondary">
      <p>No models configured. Add models to your configuration to start chatting.</p>
    </div>
  {:else}
    <!-- Messages area -->
    <div
      class="flex-1 overflow-y-auto mb-4 px-2"
      bind:this={messagesContainer}
    >
      {#if messages.length === 0}
        <div class="h-full flex items-center justify-center text-txtsecondary">
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
    <div class="shrink-0 flex gap-2">
      <ExpandableTextarea
        bind:value={userInput}
        placeholder="Type a message..."
        rows={3}
        onkeydown={handleKeyDown}
        disabled={isStreaming || !$selectedModelStore}
      />
      <div class="flex flex-col gap-2">
        {#if isStreaming}
          <button class="btn bg-red-500 hover:bg-red-600 text-white" onclick={cancelStreaming}>
            Cancel
          </button>
        {:else}
          <button
            class="btn bg-primary text-btn-primary-text hover:opacity-90"
            onclick={sendMessage}
            disabled={!userInput.trim() || !$selectedModelStore}
          >
            Send
          </button>
        {/if}
      </div>
    </div>
  {/if}
</div>
