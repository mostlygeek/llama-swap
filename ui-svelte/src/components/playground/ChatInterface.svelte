<script lang="ts">
  import { models } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion } from "../../lib/chatApi";
  import type { ChatMessage } from "../../lib/types";
  import ChatMessageComponent from "./ChatMessage.svelte";

  const selectedModelStore = persistentStore<string>("playground-selected-model", "");

  let messages = $state<ChatMessage[]>([]);
  let userInput = $state("");
  let isStreaming = $state(false);
  let abortController = $state<AbortController | null>(null);
  let messagesContainer: HTMLDivElement | undefined = $state();

  // Show all models (excluding unlisted), backend will auto-load as needed
  let availableModels = $derived($models.filter((m) => !m.unlisted));

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

    // Add empty assistant message
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    abortController = new AbortController();

    try {
      const stream = streamChatCompletion(
        $selectedModelStore,
        messages.slice(0, -1), // Send all messages except the empty assistant one
        abortController.signal
      );

      for await (const chunk of stream) {
        if (chunk.done) break;

        // Update the last message (assistant) with new content
        messages = messages.map((msg, idx) =>
          idx === messages.length - 1
            ? { ...msg, content: msg.content + chunk.content }
            : msg
        );
      }
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") {
        // User cancelled, keep partial response
      } else {
        // Show error in the assistant message
        const errorMessage = error instanceof Error ? error.message : "An error occurred";
        messages = messages.map((msg, idx) =>
          idx === messages.length - 1
            ? { ...msg, content: msg.content + `\n\n**Error:** ${errorMessage}` }
            : msg
        );
      }
    } finally {
      isStreaming = false;
      abortController = null;
    }
  }

  function cancelStreaming() {
    abortController?.abort();
  }

  function newChat() {
    if (isStreaming) {
      cancelStreaming();
    }
    messages = [];
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
  <div class="shrink-0 flex gap-2 mb-4">
    <select
      class="flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary"
      bind:value={$selectedModelStore}
      disabled={isStreaming}
    >
      <option value="">Select a model...</option>
      {#each availableModels as model (model.id)}
        <option value={model.id}>{model.name || model.id}</option>
      {/each}
    </select>
    <button class="btn" onclick={newChat} disabled={messages.length === 0 && !isStreaming}>
      New Chat
    </button>
  </div>

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
            isStreaming={isStreaming && idx === messages.length - 1 && message.role === "assistant"}
          />
        {/each}
      {/if}
    </div>

    <!-- Input area -->
    <div class="shrink-0 flex gap-2">
      <textarea
        class="flex-1 px-3 py-2 rounded border border-gray-200 dark:border-white/10 bg-surface focus:outline-none focus:ring-2 focus:ring-primary resize-none"
        placeholder="Type a message..."
        rows="3"
        bind:value={userInput}
        onkeydown={handleKeyDown}
        disabled={isStreaming || !$selectedModelStore}
      ></textarea>
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
