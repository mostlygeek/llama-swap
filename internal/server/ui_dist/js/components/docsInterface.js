// Help tab: the Docs Agent, a fixed agentic client for llama-swap's own
// documentation tools served over /api/mcp. Ported from
// components/playground/DocsInterface.svelte.
//
// Unlike the Chat tab this exposes no settings. The prompt, temperature and
// tool set are the thing being shipped — they are tuned together against
// evals/docs-agent, and a user who turns one of them down just gets worse
// answers with no way to tell why. The only choice left is the model.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { playgroundModels, hasListedModels } from "../api.js";
import { persistent } from "../store.js";
import { streamChatCompletion } from "../api/chat.js";
import { runAgent, sanitizeMessages, DEFAULT_MAX_ITERATIONS } from "../agent/agentLoop.js";
import { fetchToolDefinitions, callTool, friendlyToolName } from "../agent/agentTools.js";
import { DOCS_AGENT_SYSTEM_PROMPT } from "../agent/docsAgentPrompt.js";
import { playgroundStores } from "../playgroundActivity.js";
import { AgentWork } from "./agentWork.js";
import { renderMarkdown } from "../markdown.js";

const TEMPERATURE = 0;
const MAX_TOKENS = 4096;

const SUGGESTIONS = [
  "How do I unload a model after 5 minutes of inactivity?",
  "How do I run two models on one GPU at the same time?",
  "What models are configured on this server?",
  "My model won't load. How do I debug it?",
];

function getTextContent(content) {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .filter((p) => p?.type === "text")
      .map((p) => p.text)
      .join("");
  }
  return "";
}

function loadMessages() {
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

export function DocsInterface() {
  const selectedModelStore = persistent("playground-docs-model", "");

  let messages = loadMessages();
  let userInput = "";
  let isStreaming = false;
  let isReasoning = false;
  let reasoningStartTime = 0;
  let abortController = null;
  let userScrolledUp = false;
  let toolDefs = [];
  let toolsLoaded = false;
  let agentIteration = 0;
  let agentNotice = null;
  let showJinjaHint = false;
  let lastSaveTime = 0;

  const root = el(`
    <div class="docs">
      <div class="docs-controls">
        <select class="docs-model-select" data-model-select></select>
        <button class="btn btn--sm" data-new-chat>New Chat</button>
      </div>
      <div class="docs-messages" data-messages></div>
      <div class="docs-input-area">
        <div class="docs-jinja-hint" data-jinja-hint style="display:none">
          <span aria-hidden="true">⚠</span>
          <div>
            The model described a tool call instead of making one. llama-server needs
            <code>--jinja</code> for tool calling to work. See the
            <span class="mono">tutorials/tool-calling-setup</span> guide — ask about it here.
          </div>
          <button class="docs-hint-dismiss" data-dismiss-hint aria-label="Dismiss">✕</button>
        </div>
        <div class="docs-input-row">
          <textarea class="docs-input" rows="3" placeholder="Ask a question about llama-swap..." data-input></textarea>
          <div class="docs-input-actions">
            <button class="btn btn--sm btn--danger" data-cancel style="display:none">Cancel</button>
            <span class="muted docs-round" data-round style="display:none"></span>
            <button class="btn btn--sm" data-send>Send</button>
          </div>
        </div>
      </div>
    </div>
  `);

  const modelSelect = root.querySelector("[data-model-select]");
  const messagesEl = root.querySelector("[data-messages]");
  const inputEl = root.querySelector("[data-input]");
  const sendBtn = root.querySelector("[data-send]");
  const cancelBtn = root.querySelector("[data-cancel]");
  const roundEl = root.querySelector("[data-round]");
  const jinjaHint = root.querySelector("[data-jinja-hint]");

  function toolLabels() {
    return new Map(toolDefs.map((t) => [t.function.name, friendlyToolName(t.function.name, t.function.title)]));
  }

  function canSend() {
    return Boolean(selectedModelStore.get()) && toolDefs.length > 0 && !isStreaming;
  }

  function renderControls() {
    const playground = playgroundModels.get();
    // Prefer tool-capable models, but list everything so the user can try.
    const withTools = playground.filter((m) => m.capabilities?.function_calling);
    const list = (withTools.length > 0 ? withTools : playground).filter((m) => m.playgroundType !== "alias");

    const capModels = list.filter((m) => !m.peerID && m.playgroundType !== "selector");
    const selectors = list.filter((m) => m.playgroundType === "selector");
    const peers = list.filter((m) => m.peerID);

    modelSelect.innerHTML =
      `<option value="" ${selectedModelStore.get() === "" ? "selected" : ""} disabled>Select a tool-capable model...</option>` +
      [
        capModels.length ? { label: "Models", ids: capModels.map((m) => m.id) } : null,
        selectors.length ? { label: "Selectors", ids: selectors.map((m) => m.id) } : null,
        peers.length ? { label: "Peers", ids: peers.map((m) => m.id) } : null,
      ]
        .filter(Boolean)
        .map(
          (group) =>
            `<optgroup label="${escapeHtml(group.label)}">${group.ids
              .map((id) => `<option value="${escapeHtml(id)}" ${id === selectedModelStore.get() ? "selected" : ""}>${escapeHtml(id)}</option>`)
              .join("")}</optgroup>`
        )
        .join("");
    sendBtn.disabled = !canSend() || userInput.trim() === "";
    inputEl.disabled = isStreaming || !selectedModelStore.get();
  }

  modelSelect.addEventListener("change", () => {
    selectedModelStore.set(modelSelect.value);
    renderControls();
  });

  // A complete agent response can include several model turns and tool calls.
  // Keep that contiguous run in one card so its reasoning, work, and answer
  // read as a single response.
  function displayItems() {
    const display = [];
    const labels = toolLabels();

    for (let idx = 0; idx < messages.length; ) {
      const message = messages[idx];
      if (message.role !== "assistant") {
        if (message.role === "user" || message.role === "system") {
          display.push({ kind: "message", message, idx });
        }
        idx++;
        continue;
      }

      let userMessageIdx;
      for (let messageIdx = idx - 1; messageIdx >= 0; messageIdx--) {
        if (messages[messageIdx].role === "user") {
          userMessageIdx = messageIdx;
          break;
        }
      }

      const group = [];
      while (idx < messages.length && (messages[idx].role === "assistant" || messages[idx].role === "tool")) {
        group.push({ message: messages[idx], idx });
        idx++;
      }

      const calls = new Map();
      for (const { message: m } of group) {
        for (const call of m.tool_calls ?? []) calls.set(call.id, call.function.arguments);
      }

      const assistantTurns = group.filter(({ message: m }) => m.role === "assistant");
      const finalAssistantIdx = assistantTurns[assistantTurns.length - 1].idx;
      display.push({
        kind: "agent",
        content: assistantTurns
          .map(({ message: m }) => getTextContent(m.content))
          .filter((content) => content.trim())
          .join("\n\n"),
        workItems: group.flatMap(({ message: m, idx: messageIdx }) => {
          if (m.role === "assistant") {
            const reasoning = m.reasoning_content ?? "";
            return reasoning
              ? [
                  {
                    kind: "reasoning",
                    content: reasoning,
                    durationMs: m.reasoningTimeMs,
                    running: isReasoning && messageIdx === messages.length - 1,
                  },
                ]
              : [];
          }
          return [
            {
              kind: "tool",
              name: m.name ?? "",
              label: labels.get(m.name ?? "") ?? friendlyToolName(m.name ?? ""),
              args: calls.get(m.tool_call_id ?? "") ?? "",
              content: getTextContent(m.content),
              ok: m.toolOk,
              durationMs: m.toolDurationMs ?? 0,
              running: m.toolOk === undefined,
            },
          ];
        }),
        finalAssistantIdx,
        userMessageIdx,
        isCurrent: finalAssistantIdx === messages.length - 1,
      });
    }

    return display;
  }

  function renderMessages() {
    const items = displayItems();

    if (messages.length === 0) {
      messagesEl.innerHTML = `
        <div class="docs-empty">
          <div class="docs-empty-inner">
            <p class="docs-empty-title">Ask about llama-swap</p>
            <p class="docs-empty-sub">Answers come from this server's own documentation and its running configuration, not
            from what the model remembers.</p>
            <div class="docs-suggestions">
              ${SUGGESTIONS.map(
                (s) => `<button type="button" class="docs-suggestion" data-ask="${escapeHtml(s)}" ${!canSend() ? "disabled" : ""}>${escapeHtml(s)}</button>`
              ).join("")}
            </div>
            ${!selectedModelStore.get() ? `<p class="muted docs-empty-hint">Select a model to get started.</p>` : ""}
          </div>
        </div>`;
      return;
    }

    messagesEl.innerHTML = items
      .map((item) => {
        if (item.kind === "message") {
          if (item.message.role === "user") {
            return `<div class="docs-msg docs-msg-user"><div class="docs-bubble docs-bubble-user">${renderMarkdown(getTextContent(item.message.content))}</div></div>`;
          }
          return `<div class="docs-msg docs-msg-system"><div class="docs-bubble">${renderMarkdown(getTextContent(item.message.content))}</div></div>`;
        }
        const streaming = isStreaming && item.isCurrent;
        const reasoning = isReasoning && item.isCurrent;
        return `
          <div class="docs-msg docs-msg-assistant">
            <div class="agent-work-wrap" data-work></div>
            <div class="docs-bubble ${streaming ? "docs-bubble-streaming" : ""}" data-md></div>
            ${streaming ? "" : item.userMessageIdx !== undefined ? `<button class="docs-regen" data-regen="${item.userMessageIdx}">Regenerate</button>` : ""}
          </div>`;
      })
      .join("")
      .concat(agentNotice ? `<div class="docs-notice muted">${escapeHtml(agentNotice)}</div>` : "");

    // Fill agent cards (work items + markdown) after the innerHTML rebuild.
    const agentCards = [...messagesEl.querySelectorAll(".docs-msg-assistant")];
    const agentItems = items.filter((i) => i.kind === "agent");
    agentCards.forEach((card, i) => {
      const item = agentItems[i];
      if (!item) return;
      const work = AgentWork({ workItems: item.workItems });
      card.querySelector("[data-work]").appendChild(work.el);
      const md = card.querySelector("[data-md]");
      md.innerHTML = renderMarkdown(item.content) || (isStreaming && item.isCurrent ? "" : "<em class='muted'>(no content)</em>");
    });

    if (!userScrolledUp) {
      messagesEl.scrollTo({ top: messagesEl.scrollHeight, behavior: isStreaming ? "instant" : "smooth" });
    }
  }

  messagesEl.addEventListener("click", (e) => {
    const ask = e.target.closest("[data-ask]");
    if (ask && canSend()) {
      inputEl.value = ask.getAttribute("data-ask");
      sendMessage();
      return;
    }
    const regen = e.target.closest("[data-regen]");
    if (regen && canSend()) {
      regenerateFromIndex(Number(regen.getAttribute("data-regen")));
    }
  });

  messagesEl.addEventListener("scroll", () => {
    const { scrollTop, scrollHeight, clientHeight } = messagesEl;
    userScrolledUp = scrollHeight - scrollTop - clientHeight > 40;
  });

  function persistMessages() {
    const json = JSON.stringify(messages);
    const elapsed = Date.now() - lastSaveTime;
    const save = () => {
      try {
        localStorage.setItem("playground-docs-messages", json);
      } catch {}
      lastSaveTime = Date.now();
    };
    if (elapsed >= 2000) {
      save();
    } else {
      setTimeout(save, 2000 - elapsed);
    }
  }

  /** Merges a patch into the last message. */
  function patchLast(patch) {
    messages = messages.map((msg, i) => (i === messages.length - 1 ? { ...msg, ...patch } : msg));
  }

  function lastText() {
    const last = messages[messages.length - 1];
    return typeof last?.content === "string" ? last.content : "";
  }

  /** Applies a streamed delta to the in-progress assistant message. */
  function appendDelta(kind, text) {
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

  function appendError(message) {
    patchLast({ content: lastText() + `\n\n**Error:** ${message}` });
  }

  function handleTurnError(error) {
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
  function requestMessages() {
    return [
      { role: "system", content: DOCS_AGENT_SYSTEM_PROMPT },
      ...messages.slice(0, -1), // everything but the empty placeholder
    ];
  }

  async function regenerateFromIndex(idx) {
    if (!canSend()) return;
    // Remove all messages after the edited user message, then add an empty
    // assistant placeholder for the new response.
    messages = messages.slice(0, idx + 1);
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    isReasoning = false;
    reasoningStartTime = 0;
    agentIteration = 0;
    agentNotice = null;
    abortController = new AbortController();
    renderMessages();
    renderControls();

    try {
      await runAgentTurn(abortController.signal);
    } catch (error) {
      handleTurnError(error);
    } finally {
      isStreaming = false;
      isReasoning = false;
      abortController = null;
      playgroundStores.docsStreaming.set(false);
      renderMessages();
      renderControls();
      persistMessages();
    }
  }

  async function sendMessage() {
    const trimmedInput = userInput.trim() || inputEl.value.trim();
    if (!trimmedInput || !canSend()) return;

    userInput = "";
    inputEl.value = "";
    userScrolledUp = false;
    messages = [...messages, { role: "user", content: trimmedInput }];

    await regenerateFromIndex(messages.length - 1);
  }

  async function runAgentTurn(signal) {
    const deps = {
      streamChat: (msgs, sig) =>
        streamChatCompletion(selectedModelStore.get(), msgs, sig, {
          temperature: TEMPERATURE,
          endpoint: "v1/chat/completions",
          max_tokens: MAX_TOKENS,
          tools: toolDefs,
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
          // The initial placeholder is created before the loop starts. Later
          // iterations start only after all tool results, keeping parallel
          // calls together without empty assistant turns between them.
          if (event.n > 1) {
            messages = [...messages, { role: "assistant", content: "" }];
          }
          roundEl.style.display = agentIteration > 1 ? "" : "none";
          roundEl.textContent = `round ${agentIteration}/${DEFAULT_MAX_ITERATIONS}`;
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
            showJinjaHint = toolDefs.some(
              (tool) =>
                getTextContent(event.message.content).includes(`"${tool.function.name}"`) ||
                getTextContent(event.message.content).includes(`${tool.function.name}(`)
            );
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
      renderMessages();
    }

    // An abort, or a final turn that produced nothing, can leave the trailing
    // placeholder empty.
    const last = messages[messages.length - 1];
    if (last?.role === "assistant" && !last.tool_calls?.length && !last.reasoning_content && lastText() === "") {
      messages = messages.slice(0, -1);
    }
  }

  sendBtn.addEventListener("click", sendMessage);
  cancelBtn.addEventListener("click", () => abortController?.abort());
  inputEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      sendMessage();
    }
  });
  root.querySelector("[data-new-chat]").addEventListener("click", () => {
    if (isStreaming) abortController?.abort();
    messages = [];
    isReasoning = false;
    reasoningStartTime = 0;
    agentIteration = 0;
    agentNotice = null;
    showJinjaHint = false;
    persistMessages();
    renderMessages();
  });
  root.querySelector("[data-dismiss-hint]").addEventListener("click", () => {
    showJinjaHint = false;
    jinjaHint.style.display = "none";
  });

  const subs = [
    playgroundModels.subscribe(renderControls),
    // Reflect streaming state for the sidebar activity indicator.
  ];
  // streaming flag is pushed in regenerateFromIndex via a simple interval check
  const streamSync = setInterval(() => {
    playgroundStores.docsStreaming.set(isStreaming);
    jinjaHint.style.display = showJinjaHint ? "" : "none";
    cancelBtn.style.display = isStreaming ? "" : "none";
    sendBtn.style.display = isStreaming ? "none" : "";
  }, 300);

  fetchToolDefinitions()
    .then((defs) => (toolDefs = defs))
    .catch(() => (toolDefs = []))
    .finally(() => {
      toolsLoaded = true;
      renderControls();
      renderMessages();
    });

  renderControls();
  renderMessages();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
      clearInterval(streamSync);
      abortController?.abort();
    },
  };
}
