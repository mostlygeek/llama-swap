<script lang="ts">
  import { onMount } from "svelte";
  import { hasListedModels } from "../../stores/api";
  import { persistentStore } from "../../stores/persistent";
  import { streamChatCompletion, type ToolDefinition } from "../../lib/chatApi";
  import { runAgent, sanitizeMessages, DEFAULT_MAX_ITERATIONS } from "../../lib/agentLoop";
  import { fetchToolDefinitions, callTool, friendlyToolName } from "../../lib/agentTools";
  import { DOCS_AGENT_SYSTEM_PROMPT } from "../../lib/prompts/docsAgent";
  import { pickSuggestions } from "../../lib/prompts/docsSuggestions";
  import { docsAgentStreaming } from "../../stores/playgroundActivity";
  import { getTextContent, type ChatMessage } from "../../lib/types";
  import { isSubmitEnter } from "../../lib/ime";
  import ChatMessageComponent from "./ChatMessage.svelte";
  import type { WorkItem } from "./AgentWork.svelte";
  import ModelSelector from "./ModelSelector.svelte";
  import ExpandableTextarea from "./ExpandableTextarea.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { RefreshCw, TriangleAlert, X } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";

  /**
   * The Docs Agent: a fixed agentic client for llama-swap's own documentation
   * tools. This is the whole of the Help page; routes/Help.svelte only frames
   * it. It sits among the playground components because it is built from them
   * -- the same chat message, model selector and textarea -- not because Help
   * is a playground tab. It stopped being one.
   *
   * Unlike the Chat tab this exposes no settings. The prompt and tool set are
   * the thing being shipped -- they are tuned together against
   * evals/docs-agent, and a user who turns one of them down just gets worse
   * answers with no way to tell why. Sampling params are left unset so the
   * server's own defaults apply. The only choice left is the model.
   */

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
  // Drawn once per empty chat rather than derived: a list that recomputed
  // would reshuffle under the reader's cursor.
  let suggestions = $state(pickSuggestions());
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
  let agentNotice = $state<string | null>(null);
  let showJinjaHint = $state(false);

  type DisplayMessage =
    | { kind: "message"; message: ChatMessage & { role: "user" | "system" }; idx: number }
    | {
        kind: "agent";
        content: string;
        workItems: WorkItem[];
        finalAssistantIdx: number;
        userMessageIdx: number | undefined;
        isCurrent: boolean;
      };

  // name -> friendly label, so tool-call cards can show it without re-deriving.
  let toolLabels = $derived(
    new Map(toolDefs.map((t) => [t.function.name, friendlyToolName(t.function.name, t.function.title)]))
  );
  let canSend = $derived(Boolean($selectedModelStore) && toolDefs.length > 0 && !isStreaming);

  // A complete agent response can include several model turns and tool calls.
  // Keep that contiguous run in one card so its reasoning, work, and answer
  // read as a single response.
  let displayMessages = $derived.by<DisplayMessage[]>(() => {
    const display: DisplayMessage[] = [];

    for (let idx = 0; idx < messages.length;) {
      const message = messages[idx];
      if (message.role !== "assistant") {
        // Tool results are consumed with the preceding assistant turn. An
        // orphaned result cannot occur in a valid agent conversation.
        if (message.role === "user" || message.role === "system") {
          display.push({ kind: "message", message: message as ChatMessage & { role: "user" | "system" }, idx });
        }
        idx++;
        continue;
      }

      let userMessageIdx: number | undefined;
      for (let messageIdx = idx - 1; messageIdx >= 0; messageIdx--) {
        if (messages[messageIdx].role === "user") {
          userMessageIdx = messageIdx;
          break;
        }
      }

      const group: { message: ChatMessage; idx: number }[] = [];
      while (idx < messages.length && (messages[idx].role === "assistant" || messages[idx].role === "tool")) {
        group.push({ message: messages[idx], idx });
        idx++;
      }

      const calls = new Map<string, string>();
      for (const { message } of group) {
        for (const call of message.tool_calls ?? []) calls.set(call.id, call.function.arguments);
      }

      const assistantTurns = group.filter(({ message }) => message.role === "assistant");
      const finalAssistantIdx = assistantTurns[assistantTurns.length - 1].idx;
      display.push({
        kind: "agent",
        content: assistantTurns
          .map(({ message }) => getTextContent(message.content))
          .filter((content) => content.trim())
          .join("\n\n"),
        workItems: group.flatMap<WorkItem>(({ message, idx: messageIdx }) => {
          if (message.role === "assistant") {
            const reasoning = message.reasoning_content ?? "";
            return reasoning
              ? [{
                  kind: "reasoning" as const,
                  content: reasoning,
                  durationMs: message.reasoningTimeMs,
                  running: isReasoning && messageIdx === messages.length - 1,
                }]
              : [];
          }
          return [{
            kind: "tool" as const,
            name: message.name ?? "",
            label: toolLabels.get(message.name ?? "") ?? friendlyToolName(message.name ?? ""),
            args: calls.get(message.tool_call_id ?? "") ?? "",
            content: getTextContent(message.content),
            ok: message.toolOk,
            durationMs: message.toolDurationMs ?? 0,
            running: message.toolOk === undefined,
          }];
        }),
        finalAssistantIdx,
        userMessageIdx,
        isCurrent: finalAssistantIdx === messages.length - 1,
      });
    }

    return display;
  });

  onMount(() => {
    fetchToolDefinitions()
      .then((defs) => (toolDefs = defs))
      .catch(() => (toolDefs = []))
      .finally(() => (toolsLoaded = true));
  });

  $effect(() => {
    docsAgentStreaming.set(isStreaming);
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

  function refreshSuggestions() {
    suggestions = pickSuggestions();
  }

  function cancelStreaming() {
    abortController?.abort();
  }

  function newChat() {
    if (isStreaming) {
      cancelStreaming();
    }
    messages = [];
    suggestions = pickSuggestions();
    isReasoning = false;
    reasoningStartTime = 0;
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
          endpoint: "v1/chat/completions" as const,
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
          // The initial placeholder is created before the loop starts. Later
          // iterations start only after all tool results, keeping parallel
          // calls together without empty assistant turns between them.
          if (event.n > 1) {
            messages = [...messages, { role: "assistant", content: "" }];
          }
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
          <div class="w-full max-w-xl px-4 text-center">
            <p class="text-foreground text-sm font-medium">Ask llama-swap about llama-swap</p>
            <p class="mt-1 text-sm">
              Choose a topic below or ask a question to get started.
            </p>
            <div class="mt-4 flex flex-col gap-2">
              {#each suggestions as suggestion (suggestion.number)}
                <button
                  type="button"
                  class="hover:bg-muted/70 disabled:hover:bg-transparent min-h-14 rounded-md border px-3 py-2 text-left text-sm transition-colors disabled:opacity-50"
                  disabled={!canSend}
                  onclick={() => ask(suggestion.question)}
                >
                  <!-- The number is the topic's place in the pool, not this
                       button's place on screen, so it stays muted: it labels
                       the question rather than competing with it. -->
                  <span class="text-muted-foreground tabular-nums">#{suggestion.number}.</span>
                  {suggestion.question}
                </button>
              {/each}
            </div>
            <div class="mt-2 flex justify-end">
              <Button
                variant="ghost"
                size="sm"
                class="text-muted-foreground"
                onclick={refreshSuggestions}
              >
                <RefreshCw />
                Refresh topics
              </Button>
            </div>
            {#if !$selectedModelStore}
              <p class="mt-3 text-xs">Select a model to get started.</p>
            {/if}
          </div>
        </EmptyState>
      {:else}
        {#each displayMessages as item, idx (idx)}
          {#if item.kind === "agent"}
            <ChatMessageComponent
              role="assistant"
              content={item.content}
              workItems={item.workItems}
              isStreaming={isStreaming && item.isCurrent}
              isReasoning={isReasoning && item.isCurrent}
              onRegenerate={!isStreaming && item.userMessageIdx !== undefined
                ? () => regenerateFromIndex(item.userMessageIdx!)
                : undefined}
            />
          {:else}
            <ChatMessageComponent
              role={item.message.role}
              content={item.message.content}
              reasoning_content={item.message.reasoning_content}
              reasoningTimeMs={item.message.reasoningTimeMs}
              onEdit={item.message.role === "user" ? (newContent) => editMessage(item.idx, newContent) : undefined}
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
          {:else}
            <Button onclick={sendMessage} disabled={!userInput.trim() || !canSend}>Send</Button>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
