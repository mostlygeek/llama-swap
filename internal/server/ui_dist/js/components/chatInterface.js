// Chat playground tab, ported from components/playground/ChatInterface.svelte.
// Owns the message list, streaming lifecycle (abort + reasoning timing), image
// attachments, persisted settings and messages. During streaming only the last
// assistant ChatMessage is update()d so prior messages are not re-parsed.
import { el, cleanupAll } from "../dom.js";
import { observable, persistent } from "../store.js";
import { models } from "../api.js";
import { playgroundStores } from "../playgroundActivity.js";
import { streamChatCompletion } from "../api/chat.js";
import { ChatMessage } from "./chatMessage.js";
import { ModelSelector } from "./modelSelector.js";
import { ExpandableTextarea } from "./expandableTextarea.js";

const ICON_GEAR = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" class="icon-5"><path fill-rule="evenodd" d="M8.34 1.804A1 1 0 0 1 9.32 1h1.36a1 1 0 0 1 .98.804l.295 1.473c.497.144.971.342 1.416.587l1.25-.834a1 1 0 0 1 1.262.125l.962.962a1 1 0 0 1 .125 1.262l-.834 1.25c.245.445.443.919.587 1.416l1.473.295a1 1 0 0 1 .804.98v1.36a1 1 0 0 1-.804.98l-1.473.295a6.95 6.95 0 0 1-.587 1.416l.834 1.25a1 1 0 0 1-.125 1.262l-.962.962a1 1 0 0 1-1.262.125l-1.25-.834a6.953 6.953 0 0 1-1.416.587l-.295 1.473a1 1 0 0 1-.98.804H9.32a1 1 0 0 1-.98-.804l-.295-1.473a6.957 6.957 0 0 1-1.416-.587l-1.25.834a1 1 0 0 1-1.262-.125l-.962-.962a1 1 0 0 1-.125-1.262l.834-1.25a6.957 6.957 0 0 1-.587-1.416l-1.473-.295A1 1 0 0 1 1 10.68V9.32a1 1 0 0 1 .804-.98l1.473-.295c.144-.497.342-.971.587-1.416l-.834-1.25a1 1 0 0 1 .125-1.262l.962-.962A1 1 0 0 1 5.38 3.03l1.25.834a6.957 6.957 0 0 1 1.416-.587l.294-1.473Z" clip-rule="evenodd" /></svg>`;
const ICON_ATTACH = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" class="icon-5"><path fill-rule="evenodd" d="M1 5.25A2.25 2.25 0 0 1 3.25 3h13.5A2.25 2.25 0 0 1 19 5.25v9.5A2.25 2.25 0 0 1 16.75 17H3.25A2.25 2.25 0 0 1 1 14.75v-9.5Zm1.5 5.81v3.69c0 .414.336.75.75.75h13.5a.75.75 0 0 0 .75-.75v-2.69l-2.22-2.219a.75.75 0 0 0-1.06 0l-1.91 1.909.47.47a.75.75 0 1 1-1.06 1.06L6.53 8.091a.75.75 0 0 0-1.06 0l-2.97 2.97ZM12 7a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z" clip-rule="evenodd" /></svg>`;

const ACCEPTED_IMAGE_FORMATS = ["image/jpeg", "image/png", "image/gif", "image/webp"];
const MAX_IMAGE_SIZE = 20 * 1024 * 1024; // 20MB
const MAX_IMAGES_PER_MESSAGE = 5;

function loadMessages() {
  try {
    const saved = localStorage.getItem("playground-messages");
    return saved ? JSON.parse(saved) : [];
  } catch {
    return [];
  }
}

export function ChatInterface() {
  const selectedModel = persistent("playground-selected-model", "");
  const systemPrompt = persistent("playground-system-prompt", "");
  const temperature = persistent("playground-temperature", 0.7);
  const endpoint = persistent("playground-endpoint", "v1/chat/completions");
  const maxTokens = persistent("playground-max-tokens", 4096);
  const userInput = observable("");

  let messages = loadMessages();
  let components = [];
  let isStreaming = false;
  let isReasoning = false;
  let reasoningStartTime = 0;
  let abortController = null;
  let showSettings = false;
  let attachedImages = [];
  let imageError = null;
  let userScrolledUp = false;

  const root = el(`
    <div class="chat">
      <div class="chat-toolbar">
        <div class="chat-toolbar-model" data-model></div>
        <div class="chat-toolbar-btns">
          <button class="btn" data-settings title="Settings">${ICON_GEAR}</button>
          <button class="btn" data-newchat>New Chat</button>
        </div>
      </div>
      <div class="chat-settings" data-settings-panel style="display:none"></div>
      <div class="chat-nomodels" data-nomodels style="display:none">
        <p>No models configured. Add models to your configuration to start chatting.</p>
      </div>
      <div class="chat-messages" data-messages></div>
      <div class="chat-input" data-input>
        <div class="chat-img-strip" data-imgstrip style="display:none"></div>
        <div class="chat-img-error" data-imgerror style="display:none"></div>
        <div class="chat-input-row">
          <input type="file" accept=".jpg,.jpeg,.png,.gif,.webp" multiple class="chat-file" data-file />
          <div class="chat-input-textarea" data-textarea></div>
          <div class="chat-input-actions" data-actions></div>
        </div>
      </div>
    </div>
  `);

  const modelHost = root.querySelector("[data-model]");
  const settingsBtn = root.querySelector("[data-settings]");
  const newChatBtn = root.querySelector("[data-newchat]");
  const settingsPanel = root.querySelector("[data-settings-panel]");
  const noModelsEl = root.querySelector("[data-nomodels]");
  const messagesEl = root.querySelector("[data-messages]");
  const inputEl = root.querySelector("[data-input]");
  const imgStripEl = root.querySelector("[data-imgstrip]");
  const imgErrorEl = root.querySelector("[data-imgerror]");
  const fileInput = root.querySelector("[data-file]");
  const textareaHost = root.querySelector("[data-textarea]");
  const actionsEl = root.querySelector("[data-actions]");

  const modelSel = ModelSelector({ value: selectedModel, placeholder: "Select a model...", disabled: false });
  modelHost.appendChild(modelSel.el);

  const textarea = ExpandableTextarea({
    value: userInput,
    placeholder: "Type a message...",
    rows: 3,
    disabled: false,
    onkeydown: (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        sendMessage();
      }
    },
  });
  textareaHost.appendChild(textarea.el);

  // ---- message list ----
  function propsFor(i) {
    const m = messages[i];
    const last = i === messages.length - 1;
    return {
      role: m.role,
      content: m.content,
      reasoning_content: m.reasoning_content || "",
      reasoningTimeMs: m.reasoningTimeMs || 0,
      isStreaming: isStreaming && last && m.role === "assistant",
      isReasoning: isReasoning && last && m.role === "assistant",
      onEdit: m.role === "user" ? (nc) => editMessage(i, nc) : undefined,
      onRegenerate:
        m.role === "assistant" && i > 0 && messages[i - 1].role === "user"
          ? () => regenerateFromIndex(i - 1)
          : undefined,
    };
  }

  function mountMessages() {
    components.forEach((c) => c.destroy());
    components = [];
    messagesEl.innerHTML = "";
    if (messages.length === 0) {
      messagesEl.innerHTML = `<div class="chat-empty"><p>Start a conversation by typing a message below.</p></div>`;
      return;
    }
    for (let i = 0; i < messages.length; i++) {
      const c = ChatMessage(propsFor(i));
      components.push(c);
      messagesEl.appendChild(c.el);
    }
    maybeScroll();
  }

  function updateLast() {
    const i = messages.length - 1;
    if (i >= 0 && components[i]) {
      components[i].update(propsFor(i));
      maybeScroll();
    }
  }

  function maybeScroll() {
    if (userScrolledUp) return;
    messagesEl.scrollTo({ top: messagesEl.scrollHeight, behavior: isStreaming ? "instant" : "smooth" });
  }

  messagesEl.addEventListener("scroll", () => {
    const { scrollTop, scrollHeight, clientHeight } = messagesEl;
    userScrolledUp = scrollHeight - scrollTop - clientHeight > 40;
  });

  // ---- persistence (throttled to once per 2s) ----
  let lastSaveTime = 0;
  let saveTimer = null;
  function saveMessages() {
    const json = JSON.stringify(messages);
    const elapsed = Date.now() - lastSaveTime;
    const doSave = () => {
      try {
        localStorage.setItem("playground-messages", json);
      } catch {
        /* ignore quota errors */
      }
      lastSaveTime = Date.now();
    };
    if (saveTimer) {
      clearTimeout(saveTimer);
      saveTimer = null;
    }
    if (elapsed >= 2000) {
      doSave();
    } else {
      saveTimer = setTimeout(doSave, 2000 - elapsed);
    }
  }

  // ---- streaming ----
  function sendMessage() {
    const trimmed = userInput.get().trim();
    if ((!trimmed && attachedImages.length === 0) || !selectedModel.get() || isStreaming) return;
    userScrolledUp = false;

    let content;
    if (attachedImages.length > 0) {
      const parts = [];
      if (trimmed) parts.push({ type: "text", text: trimmed });
      for (const url of attachedImages) parts.push({ type: "image_url", image_url: { url } });
      content = parts;
    } else {
      content = trimmed;
    }

    messages = [...messages, { role: "user", content }];
    userInput.set("");
    attachedImages = [];
    imageError = null;
    renderImageStrip();
    renderImageError();
    regenerateFromIndex(messages.length - 1);
  }

  function cancelStreaming() {
    abortController?.abort();
  }

  function newChat() {
    if (isStreaming) cancelStreaming();
    messages = [];
    isReasoning = false;
    reasoningStartTime = 0;
    mountMessages();
    saveMessages();
    renderActions();
    updateNewChatBtn();
  }

  async function regenerateFromIndex(idx) {
    messages = messages.slice(0, idx + 1);
    messages = [...messages, { role: "assistant", content: "" }];

    isStreaming = true;
    isReasoning = false;
    reasoningStartTime = 0;
    abortController = new AbortController();
    playgroundStores.chatStreaming.set(true);
    setControlsDisabled(true);
    mountMessages();
    renderActions();
    updateNewChatBtn();

    try {
      const apiMessages = [];
      if (systemPrompt.get().trim()) {
        apiMessages.push({ role: "system", content: systemPrompt.get().trim() });
      }
      apiMessages.push(...messages.slice(0, -1));

      const stream = streamChatCompletion(selectedModel.get(), apiMessages, abortController.signal, {
        temperature: temperature.get(),
        endpoint: endpoint.get(),
        max_tokens: maxTokens.get(),
      });

      const lastIdx = messages.length - 1;
      for await (const chunk of stream) {
        if (chunk.done) break;

        if (chunk.reasoning_content) {
          if (!isReasoning) {
            isReasoning = true;
            reasoningStartTime = Date.now();
          }
          const m = messages[lastIdx];
          m.reasoning_content = (m.reasoning_content || "") + chunk.reasoning_content;
        }

        if (chunk.content) {
          if (isReasoning) {
            messages[lastIdx].reasoningTimeMs = Date.now() - reasoningStartTime;
            isReasoning = false;
          }
          messages[lastIdx].content = messages[lastIdx].content + chunk.content;
        }

        if (chunk.reasoning_content || chunk.content) {
          updateLast();
          saveMessages();
        }
      }
    } catch (error) {
      const lastIdx = messages.length - 1;
      if (error && error.name === "AbortError") {
        if (isReasoning && reasoningStartTime > 0) {
          messages[lastIdx].reasoningTimeMs = Date.now() - reasoningStartTime;
        }
      } else {
        const msg = error instanceof Error ? error.message : "An error occurred";
        messages[lastIdx].content = messages[lastIdx].content + `\n\n**Error:** ${msg}`;
      }
    } finally {
      isStreaming = false;
      isReasoning = false;
      abortController = null;
      playgroundStores.chatStreaming.set(false);
      setControlsDisabled(false);
      updateLast();
      renderActions();
      updateNewChatBtn();
      saveMessages();
    }
  }

  function editMessage(idx, newContent) {
    if (isStreaming || !selectedModel.get()) return;
    messages[idx] = { ...messages[idx], content: newContent };
    regenerateFromIndex(idx);
  }

  // ---- settings panel ----
  function renderSettings() {
    settingsPanel.style.display = showSettings ? "" : "none";
    if (!showSettings) {
      settingsPanel.innerHTML = "";
      return;
    }
    settingsPanel.innerHTML = `
      <div class="chat-setting">
        <label class="chat-setting-label" for="chat-endpoint">Endpoint</label>
        <select id="chat-endpoint" class="pg-input chat-setting-input" data-k="endpoint">
          <option value="v1/chat/completions">/v1/chat/completions</option>
          <option value="v1/messages">/v1/messages</option>
          <option value="v1/responses">/v1/responses</option>
        </select>
      </div>
      <div class="chat-setting">
        <label class="chat-setting-label" for="chat-system">System Prompt</label>
        <textarea id="chat-system" class="pg-input chat-setting-input chat-setting-textarea" rows="3" placeholder="You are a helpful assistant..." data-k="system"></textarea>
      </div>
      <div class="chat-setting">
        <label class="chat-setting-label" for="chat-temp">Temperature: <span data-temp-val>${temperature.get().toFixed(2)}</span></label>
        <input id="chat-temp" type="range" min="0" max="2" step="0.05" class="chat-setting-range" data-k="temperature" />
        <div class="chat-setting-range-labels"><span>Precise (0)</span><span>Creative (2)</span></div>
      </div>
      <div class="chat-setting">
        <label class="chat-setting-label" for="chat-maxtokens">Max Tokens</label>
        <input id="chat-maxtokens" type="number" min="1" class="pg-input chat-setting-input" data-k="maxTokens" />
        <p class="chat-setting-help">Required for /v1/messages.</p>
      </div>`;
    settingsPanel.querySelector('[data-k="endpoint"]').value = endpoint.get();
    settingsPanel.querySelector('[data-k="system"]').value = systemPrompt.get();
    settingsPanel.querySelector('[data-k="temperature"]').value = String(temperature.get());
    settingsPanel.querySelector('[data-k="maxTokens"]').value = String(maxTokens.get());
    setControlsDisabled(isStreaming);
  }

  settingsPanel.addEventListener("input", (e) => {
    const k = e.target.getAttribute("data-k");
    if (k === "endpoint") endpoint.set(e.target.value);
    else if (k === "system") systemPrompt.set(e.target.value);
    else if (k === "temperature") {
      const v = Number(e.target.value);
      temperature.set(v);
      const lbl = settingsPanel.querySelector("[data-temp-val]");
      if (lbl) lbl.textContent = v.toFixed(2);
    } else if (k === "maxTokens") maxTokens.set(Number(e.target.value));
  });

  function setControlsDisabled(disabled) {
    modelSel.setDisabled(disabled);
    settingsPanel.querySelectorAll("select, textarea, input").forEach((n) => {
      n.disabled = disabled;
    });
  }

  // ---- input actions ----
  function renderActions() {
    const canSend = (userInput.get().trim() || attachedImages.length > 0) && selectedModel.get();
    if (isStreaming) {
      actionsEl.innerHTML = `<button class="btn pg-btn-cancel" data-cancel>Cancel</button>`;
    } else {
      actionsEl.innerHTML = `
        <button class="btn" data-attach title="Attach image">${ICON_ATTACH}</button>
        <button class="btn btn--primary" data-send>Send</button>`;
      actionsEl.querySelector("[data-attach]").disabled = !selectedModel.get();
      actionsEl.querySelector("[data-send]").disabled = !canSend;
    }
    textarea.setDisabled(isStreaming || !selectedModel.get());
  }

  actionsEl.addEventListener("click", (e) => {
    if (e.target.closest("[data-cancel]")) cancelStreaming();
    else if (e.target.closest("[data-attach]")) fileInput.click();
    else if (e.target.closest("[data-send]")) sendMessage();
  });

  // ---- image attachments ----
  function validateImageFile(file) {
    if (!ACCEPTED_IMAGE_FORMATS.includes(file.type)) {
      return `Invalid file type: ${file.type}. Accepted formats: JPG, PNG, GIF, WEBP`;
    }
    if (file.size > MAX_IMAGE_SIZE) {
      return `File too large: ${(file.size / 1024 / 1024).toFixed(1)}MB. Maximum size: 20MB`;
    }
    return null;
  }

  function fileToDataUrl(file) {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result);
      reader.onerror = () => reject(new Error("Failed to read file"));
      reader.readAsDataURL(file);
    });
  }

  async function processImageFiles(files) {
    imageError = null;
    if (attachedImages.length + files.length > MAX_IMAGES_PER_MESSAGE) {
      imageError = `Maximum ${MAX_IMAGES_PER_MESSAGE} images per message`;
      renderImageError();
      return;
    }
    for (const file of files) {
      const err = validateImageFile(file);
      if (err) {
        imageError = err;
        renderImageError();
        return;
      }
    }
    try {
      const dataUrls = await Promise.all(files.map(fileToDataUrl));
      attachedImages = [...attachedImages, ...dataUrls];
    } catch (error) {
      imageError = error instanceof Error ? error.message : "Failed to process images";
    }
    renderImageStrip();
    renderImageError();
    renderActions();
  }

  fileInput.addEventListener("change", (e) => {
    const input = e.target;
    if (input.files && input.files.length > 0) processImageFiles(Array.from(input.files));
    input.value = "";
  });

  function renderImageStrip() {
    imgStripEl.style.display = attachedImages.length ? "" : "none";
    imgStripEl.innerHTML = attachedImages
      .map(
        (url, i) =>
          `<div class="chat-img-thumb"><img src="${url}" alt="Attached image ${i + 1}" /><button class="chat-img-remove" data-rm="${i}" title="Remove image">×</button></div>`
      )
      .join("");
  }
  imgStripEl.addEventListener("click", (e) => {
    const b = e.target.closest("[data-rm]");
    if (!b) return;
    const i = Number(b.getAttribute("data-rm"));
    attachedImages = attachedImages.filter((_, idx) => idx !== i);
    imageError = null;
    renderImageStrip();
    renderImageError();
    renderActions();
  });

  function renderImageError() {
    imgErrorEl.style.display = imageError ? "" : "none";
    imgErrorEl.textContent = imageError || "";
  }

  // ---- wiring ----
  settingsBtn.addEventListener("click", () => {
    showSettings = !showSettings;
    renderSettings();
  });
  newChatBtn.addEventListener("click", newChat);

  function updateNewChatBtn() {
    newChatBtn.disabled = messages.length === 0 && !isStreaming;
  }

  const subs = [
    models.subscribe(() => {
      const hasModels = models.get().some((m) => !m.unlisted);
      noModelsEl.style.display = hasModels ? "none" : "";
      messagesEl.style.display = hasModels ? "" : "none";
      inputEl.style.display = hasModels ? "" : "none";
    }),
    selectedModel.subscribe(() => renderActions()),
    userInput.subscribe(() => {
      renderActions();
      updateNewChatBtn();
    }),
  ];

  // initial render
  mountMessages();
  renderActions();
  renderImageStrip();
  renderImageError();
  updateNewChatBtn();

  return {
    el: root,
    destroy() {
      if (isStreaming) cancelStreaming();
      if (saveTimer) clearTimeout(saveTimer);
      components.forEach((c) => c.destroy());
      cleanupAll([modelSel.destroy, textarea.destroy, ...subs]);
    },
  };
}
