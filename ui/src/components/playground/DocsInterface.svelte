<script lang="ts">
  import { onMount } from "svelte";
  import { hasListedModels } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion, type ToolDefinition } from "../../lib/chatApi";
  import { runAgent, sanitizeMessages, DEFAULT_MAX_ITERATIONS } from "../../lib/agentLoop";
  import { fetchToolDefinitions, callTool, friendlyToolName } from "../../lib/agentTools";
  import { DOCS_AGENT_SYSTEM_PROMPT } from "../../lib/prompts/docsAgent";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import { getTextContent, isToolCallOnlyTurn, type ChatMessage } from "../../lib/types";
  import { isSubmitEnter } from "../../lib/ime";
  import ChatMessageComponent from "./ChatMessage.svelte";
  import ToolCallCard from "./ToolCallCard.svelte";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { Wrench, TriangleAlert, X } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";

  /**
   * The Docs Agent: a fixed agentic client for llama-swap's own documentation
   * tools.
   *
   * Unlike the Chat tab this exposes no settings. The prompt, temperature and
   * tool set are the thing being shipped -- they are tuned together against
   * evals/docs-agent, and a user who turns one of them down just gets worse
   * answers with no way to tell why. The only choice left is the model.
   */
  const TEMPERATURE = 0;
  const MAX_TOKENS = 4096;

  const SUGGESTIONS = [
    "How do I unload a model after 5 minutes of inactivity?",
    "How do I run two models on one GPU at the same time?",
    "What models are configured on this server?",
    "My model won't load. How do I debug it?",
  ];

  const selectedModelStore = persistentStore<string>("playground-docs-model", "");

  function loadMessages(): ChatMessage[] {
    try {
      const saved = localStorage.getItem("playground-docs-messages");
      // sanitizeMessages prunes tool calls a reload or cancel left unanswered.
      // Sending one of those upstream is a hard 400, which reads to the user
      // as a chat that is broken and will not recover.
      return saved ? sanitizeMessages(JSON.parse(saved)) : [];
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

  let userScrolledUp = $state(false);

  let toolDefs = $state<ToolDefinition[]>([]);
  let toolsLoaded = $state(false);
  let agentIteration = $state(0);
  let agentNotice = $state<string | null>(null);
  let showJinjaHint = $state(false);

  // name -> friendly label, so tool-call cards can show it without re-deriving.
  let toolLabels = $derived(
    new Map(toolDefs.map((t) => [t.function.name, friendlyToolName(t.function.name, t.function.title)]))
  );
  let canSend = $derived(Boolean($selectedModelStore) && toolDefs.length > 0 && !isStreaming);

  onMount(() => {
    fetchToolDefinitions()
      .then((defs) => (toolDefs = defs))
      .catch(() => (toolDefs = []))
      .finally(() => (toolsLoaded = true));
  });

  $effect(() => {
    playgroundStores.docsStreaming.set(isStreaming);
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
      try { localStorage.setItem("playground-docs-messages", json); } catch {}
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
    if (!trimmedInput || !canSend) return;

    userScrolledUp = false;
    messages = [...messages, { role: "user", content: trimmedInput }];
    userInput = "";

    await regenerateFromIndex(messages.length - 1);
  }

  function ask(question: string) {
    if (!canSend) return;
    userInput = question;
    void sendMessage();
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
    agentIteration = 0;
    agentNotice = null;
    showJinjaHint = false;
  }

  /** Merges a patch into the last message. */
  function patchLast(patch: Partial<ChatMessage>) {
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

  function appendError(message: string) {
    patchLast({ content: lastText() + `\n\n**Error:** ${message}` });
  }

  function handleTurnError(error: unknown) {
    if (error instanceof Error && error.name === "AbortError") {
      // Cancelled by the user: keep the partial response.
      if (isReasoning && reasoningStartTime > 0) {
        patchLast({ reasoningTimeMs: Date.now() - reasoningStartTime });
      }
      return;
    }
    appendError(error instanceof Error ? error.message : "An error occurred");
  }

  /** The conversation to send, with the Docs Agent's prompt prepended. */
  function requestMessages(): ChatMessage[] {
    return [
      { role: "system", content: DOCS_AGENT_SYSTEM_PROMPT },
      ...messages.slice(0, -1), // everything but the empty placeholder
    ];
  }

  async function regenerateFromIndex(idx: number) {
    // Remove all messages after the edited user message
    messages = messages.slice(0, idx + 1);

    // Add empty assistant message for the new response
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    isReasoning = false;
    reasoningStartTime = 0;
    agentIteration = 0;
    agentNotice = null;
    abortController = new AbortController();

    try {
      await runAgentTurn(abortController.signal);
    } catch (error) {
      handleTurnError(error);
    } finally {
      isStreaming = false;
      isReasoning = false;
      abortController = null;
    }
  }

  async function runAgentTurn(signal: AbortSignal) {
    const tools = toolDefs;
    const deps = {
      streamChat: (msgs: ChatMessage[], sig: AbortSignal) =>
        streamChatCompletion($selectedModelStore, msgs, sig, {
          temperature: TEMPERATURE,
          endpoint: "v1/chat/completions" as const,
          max_tokens: MAX_TOKENS,
          tools,
        }),
      callTool,
    };

    let sawToolCall = false;

    for await (const event of runAgent(requestMessages(), deps, {
      maxIterations: DEFAULT_MAX_ITERATIONS,
      signal,
    })) {
      switch (event.type) {
        case "iteration":
          agentIteration = event.n;
          break;

        case "content":
        case "reasoning":
          appendDelta(event.type === "content" ? "content" : "reasoning", event.delta);
          break;

        case "assistant_end":
          if (isReasoning) {
            patchLast({ reasoningTimeMs: Date.now() - reasoningStartTime });
            isReasoning = false;
          }
          patchLast({ tool_calls: event.message.tool_calls });
          if (event.message.tool_calls?.length) {
            sawToolCall = true;
          } else if (!sawToolCall) {
            // llama.cpp without --jinja ignores `tools` entirely and answers in
            // prose, with no error anywhere. Catch the shape of that.
            showJinjaHint = looksLikeUnparsedToolCall(getTextContent(event.message.content));
          }
          break;

        case "tool_start":
          messages = [
            ...messages,
            { role: "tool", tool_call_id: event.call.id, name: event.call.function.name, content: "" },
          ];
          break;

        case "tool_end":
          patchLast(event.message);
          // Placeholder for the model's next turn.
          messages = [...messages, { role: "assistant", content: "" }];
          break;

        case "max_iterations":
          agentNotice =
            `Stopped after ${event.n} tool ${event.n === 1 ? "round" : "rounds"}. ` +
            `Send another message to continue.`;
          break;

        case "error":
          appendError(event.message);
          break;
      }
    }

    // An abort, or a final turn that produced nothing, can leave the trailing
    // placeholder empty.
    const last = messages[messages.length - 1];
    if (last?.role === "assistant" && !last.tool_calls?.length && !last.reasoning_content && lastText() === "") {
      messages = messages.slice(0, -1);
    }
  }

  function looksLikeUnparsedToolCall(text: string): boolean {
    return toolDefs.some(
      (tool) => text.includes(`"${tool.function.name}"`) || text.includes(`${tool.function.name}(`)
    );
  }

  /** Arguments for a tool result, found on the assistant turn that requested it. */
  function argsForToolMessage(idx: number): string {
    const id = messages[idx]?.tool_call_id;
    for (let i = idx - 1; i >= 0; i--) {
      const call = messages[i].tool_calls?.find((c) => c.id === id);
      if (call) return call.function.arguments;
      if (messages[i].role !== "tool") break;
    }
    return "";
  }

  async function editMessage(idx: number, newContent: string) {
    if (!canSend) return;

    messages = messages.map((msg, i) => (i === idx ? { ...msg, content: newContent } : msg));
    await regenerateFromIndex(idx);
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (isSubmitEnter(event)) {
      event.preventDefault();
      sendMessage();
    }
  }
</script>

<div class="flex h-full flex-col">
  <!-- Model selector and controls -->
  <div class="mb-4 shrink-0">
    <div class="flex flex-wrap gap-2">
      <ModelSelector
        bind:value={$selectedModelStore}
        placeholder="Select a tool-capable model..."
        disabled={isStreaming}
        capabilities={["function_calling"]}
      />
      <Button variant="outline" onclick={newChat} disabled={messages.length === 0 && !isStreaming}>
        New Chat
      </Button>
    </div>

    <!--
      The tool list is shown, not configured: it is what makes the answers
      trustworthy, so it is worth seeing, but turning one off only breaks the
      agent.
    -->
    {#if toolDefs.length > 0}
      <div class="text-muted-foreground mt-2 flex flex-wrap items-center gap-1.5 text-xs">
        <Wrench class="size-3.5 shrink-0" />
        <span>Answers using</span>
        {#each toolDefs as tool (tool.function.name)}
          <span class="bg-muted/60 rounded px-1.5 py-0.5" title={tool.function.description}>
            {friendlyToolName(tool.function.name, tool.function.title)}
          </span>
        {/each}
      </div>
    {/if}
  </div>

  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add models to your configuration to start chatting." />
  {:else if toolsLoaded && toolDefs.length === 0}
    <EmptyState
      full
      message="This server has no documentation indexed, so the docs tools are unavailable."
    />
  {:else}
    <!-- Messages area -->
    <div
      class="mb-4 flex-1 overflow-y-auto px-2"
      bind:this={messagesContainer}
      onscroll={handleMessagesScroll}
    >
      {#if messages.length === 0}
        <EmptyState full>
          <div class="max-w-md px-4 text-center">
            <p class="text-foreground text-sm font-medium">Ask about llama-swap</p>
            <p class="mt-1 text-sm">
              Answers come from this server's own documentation and its running configuration, not
              from what the model remembers.
            </p>
            <div class="mt-4 flex flex-col gap-2">
              {#each SUGGESTIONS as suggestion (suggestion)}
                <button
                  type="button"
                  class="hover:bg-muted/70 disabled:hover:bg-transparent rounded-md border px-3 py-2 text-left text-sm transition-colors disabled:opacity-50"
                  disabled={!canSend}
                  onclick={() => ask(suggestion)}
                >
                  {suggestion}
                </button>
              {/each}
            </div>
            {#if !$selectedModelStore}
              <p class="mt-3 text-xs">Select a model to get started.</p>
            {/if}
          </div>
        </EmptyState>
      {:else}
        {#each messages as message, idx (idx)}
          {#if message.role === "tool"}
            <ToolCallCard
              name={message.name ?? ""}
              label={toolLabels.get(message.name ?? "") ?? friendlyToolName(message.name ?? "")}
              args={argsForToolMessage(idx)}
              content={getTextContent(message.content)}
              ok={message.toolOk}
              durationMs={message.toolDurationMs ?? 0}
              running={message.toolOk === undefined}
            />
          {:else if isToolCallOnlyTurn(message)}
            <!-- A turn that only made tool calls has nothing to show; its cards follow. -->
          {:else}
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
          {/if}
        {/each}

        {#if agentNotice}
          <div class="text-muted-foreground bg-muted/40 mb-2 rounded-md border px-3 py-2 text-xs">
            {agentNotice}
          </div>
        {/if}
      {/if}
    </div>

    <!-- Input area -->
    <div class="shrink-0">
      {#if showJinjaHint}
        <div class="mb-2 flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs">
          <TriangleAlert class="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <div class="min-w-0 flex-1">
            The model described a tool call instead of making one. llama-server needs
            <code class="font-mono">--jinja</code> for tool calling to work. See the
            <span class="font-mono">tutorials/tool-calling-setup</span> guide — ask about it here.
          </div>
          <Button variant="ghost" size="icon-sm" onclick={() => (showJinjaHint = false)} title="Dismiss">
            <X class="size-3" />
          </Button>
        </div>
      {/if}

      <div class="flex gap-2">
        <ExpandableTextarea
          bind:ref={inputRef}
          bind:value={userInput}
          placeholder="Ask a question about llama-swap..."
          rows={3}
          onkeydown={handleKeyDown}
          disabled={isStreaming || !$selectedModelStore}
        />
        <div class="flex flex-col gap-2">
          {#if isStreaming}
            <Button variant="destructive" onclick={cancelStreaming}>Cancel</Button>
            {#if agentIteration > 1}
              <span class="text-muted-foreground text-center text-xs tabular-nums">
                round {agentIteration}/{DEFAULT_MAX_ITERATIONS}
              </span>
            {/if}
          {:else}
            <Button onclick={sendMessage} disabled={!userInput.trim() || !canSend}>Send</Button>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
