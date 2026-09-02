// Chat message bubble, ported from components/playground/ChatMessage.svelte.
// User/system messages render as escaped pre-wrap text (with optional edit and
// image thumbnails); assistant messages render streamed markdown through the
// StreamingCache, appending one <div> per settled block and replacing only the
// trailing pending block (preserving the incremental-render optimization). The
// assistant skeleton is built once and patched in place on update() so the
// codeBlockCopy MutationObserver and settled DOM survive every streaming chunk.
import { el, cleanupAll } from "../dom.js";
import {
  renderMarkdown,
  escapeHtml,
  renderStreamingMarkdown,
  createStreamingCache,
} from "../markdown.js";
import { getTextContent, getImageUrls } from "../util/content.js";

const ICON_COPY = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>`;
const ICON_CHECK = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>`;
const ICON_PENCIL = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/></svg>`;
const ICON_X = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`;
const ICON_SAVE = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z"/><path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7"/><path d="M7 3v4a1 1 0 0 0 1 1h7"/></svg>`;
const ICON_REGEN = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>`;
const ICON_CHEVRON_DOWN = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`;
const ICON_CHEVRON_RIGHT = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`;
const ICON_BRAIN = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5a3 3 0 1 0-5.997.125 4 4 0 0 0-2.526 5.77 4 4 0 0 0 .556 6.588A4 4 0 1 0 12 18Z"/><path d="M12 5a3 3 0 1 1 5.997.125 4 4 0 0 1 2.526 5.77 4 4 0 0 1-.556 6.588A4 4 0 1 1 12 18Z"/></svg>`;
const ICON_CODE = `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/></svg>`;

function formatDuration(ms) {
  if (ms < 1000) return `${ms.toFixed(0)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
    } else {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.cssText = "position:fixed;left:-9999px";
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      document.body.removeChild(ta);
    }
    return true;
  } catch (e) {
    console.error("copy failed", e);
    return false;
  }
}

// Attach copy buttons to any <pre> the renderer produces. Returns a disconnect fn.
function codeBlockCopy(node) {
  function attach() {
    node.querySelectorAll("pre:not([data-copy-btn])").forEach((pre) => {
      pre.setAttribute("data-copy-btn", "true");
      const btn = document.createElement("button");
      btn.className = "code-copy-btn";
      btn.title = "Copy code";
      btn.innerHTML = ICON_COPY;
      btn.addEventListener("click", async () => {
        const text = pre.querySelector("code")?.textContent ?? pre.textContent ?? "";
        if (await copyText(text)) {
          btn.innerHTML = ICON_CHECK;
          btn.classList.add("copied");
          setTimeout(() => {
            btn.innerHTML = ICON_COPY;
            btn.classList.remove("copied");
          }, 2000);
        }
      });
      pre.appendChild(btn);
    });
  }
  attach();
  const mo = new MutationObserver(attach);
  mo.observe(node, { childList: true, subtree: true });
  return () => mo.disconnect();
}

function openImageModal(url) {
  const overlay = el(`
    <div class="chat-img-modal" role="button" tabindex="-1">
      <button class="chat-img-modal-close" title="Close">${ICON_X}</button>
      <img src="${escapeHtml(url)}" alt="" class="chat-img-modal-img" />
    </div>
  `);
  document.body.style.overflow = "hidden";
  function close() {
    overlay.remove();
    document.body.style.overflow = "";
    document.removeEventListener("keydown", onKey);
  }
  function onKey(e) {
    if (e.key === "Escape") close();
  }
  overlay.addEventListener("click", (e) => {
    if (e.target === overlay || e.target.closest(".chat-img-modal-close")) close();
  });
  document.addEventListener("keydown", onKey);
  document.body.appendChild(overlay);
}

function imagesHtml(urls) {
  return urls
    .map(
      (u, i) =>
        `<button class="chat-img-btn" data-img="${i}"><img src="${escapeHtml(u)}" alt="Image ${i + 1}" /></button>`
    )
    .join("");
}

function reconcileSettled(container, blocks) {
  const kids = container.children;
  let i = 0;
  while (i < blocks.length && i < kids.length && kids[i].getAttribute("data-bid") === String(blocks[i].id)) {
    i++;
  }
  while (kids.length > i) container.removeChild(kids[kids.length - 1]);
  for (; i < blocks.length; i++) {
    const d = document.createElement("div");
    d.setAttribute("data-bid", String(blocks[i].id));
    d.innerHTML = blocks[i].html;
    container.appendChild(d);
  }
}

export function ChatMessage(initial) {
  let props = { reasoning_content: "", reasoningTimeMs: 0, isStreaming: false, isReasoning: false, ...initial };
  let showReasoning = false;
  let showRaw = false;
  let isEditing = false;
  let copied = false;
  let cache = createStreamingCache();
  let lastImagesKey = null; // avoid rebuilding image thumbnails every chunk

  const root = el(`<div class="chat-msg"></div>`);
  let proseDisconnect = null;

  // ===== assistant =====
  function buildAssistant() {
    root.className = "chat-msg chat-msg-assistant";
    root.innerHTML = `
      <div class="chat-bubble chat-bubble-assistant">
        <div class="chat-reasoning" data-reasoning style="display:none"></div>
        <div class="chat-images chat-images-assistant" data-images style="display:none"></div>
        <div class="chat-raw" data-raw style="display:none"></div>
        <div class="prose" data-prose>
          <div data-blocks></div>
          <div data-pending></div>
          <span class="chat-cursor" data-cursor style="display:none"></span>
        </div>
        <div class="chat-actions" data-actions style="display:none"></div>
      </div>`;
    proseDisconnect = codeBlockCopy(root.querySelector("[data-prose]"));

    root.querySelector("[data-reasoning]").addEventListener("click", (e) => {
      if (e.target.closest("[data-reasoning-toggle]")) {
        showReasoning = !showReasoning;
        renderReasoning();
      }
    });
    root.querySelector("[data-images]").addEventListener("click", (e) => {
      const b = e.target.closest("[data-img]");
      if (b) openImageModal(getImageUrls(props.content)[Number(b.getAttribute("data-img"))]);
    });
    root.querySelector("[data-actions]").addEventListener("click", (e) => {
      if (e.target.closest("[data-regen]")) props.onRegenerate?.();
      else if (e.target.closest("[data-copy]")) onCopy();
      else if (e.target.closest("[data-rawtoggle]")) {
        showRaw = !showRaw;
        renderProse();
        renderActions();
      }
    });
  }

  function renderReasoning() {
    const host = root.querySelector("[data-reasoning]");
    const open = props.reasoning_content || props.isReasoning;
    host.style.display = open ? "" : "none";
    if (!open) {
      host.innerHTML = "";
      return;
    }
    const rc = props.reasoning_content || "";
    const meta = `(${rc.length} chars${!props.isReasoning && props.reasoningTimeMs > 0 ? `, ${formatDuration(props.reasoningTimeMs)}` : ""})`;
    host.innerHTML = `
      <button class="chat-reasoning-btn" data-reasoning-toggle>
        ${showReasoning ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT}
        ${ICON_BRAIN}
        <span class="chat-reasoning-label">Reasoning</span>
        <span class="chat-reasoning-meta">${meta}</span>
        ${props.isReasoning ? `<span class="chat-reasoning-live"><span class="chat-reasoning-dot"></span>reasoning...</span>` : ""}
      </button>
      ${showReasoning ? `<div class="chat-reasoning-body" data-reasoning-body></div>` : ""}`;
    if (showReasoning) {
      const body = host.querySelector("[data-reasoning-body]");
      body.textContent = rc;
      if (props.isReasoning) {
        const cur = document.createElement("span");
        cur.className = "chat-cursor chat-cursor-thin";
        body.appendChild(cur);
      }
    }
  }

  function renderImages() {
    const host = root.querySelector("[data-images]");
    const urls = getImageUrls(props.content);
    const key = urls.join("|");
    if (key === lastImagesKey) return;
    lastImagesKey = key;
    host.style.display = urls.length ? "" : "none";
    host.innerHTML = urls.length ? imagesHtml(urls) : "";
  }

  function renderProse() {
    const text = getTextContent(props.content);
    const raw = root.querySelector("[data-raw]");
    const prose = root.querySelector("[data-prose]");
    if (showRaw) {
      raw.style.display = "";
      raw.textContent = text;
      prose.style.display = "none";
      return;
    }
    raw.style.display = "none";
    prose.style.display = "";

    const blocksEl = prose.querySelector("[data-blocks]");
    const pendingEl = prose.querySelector("[data-pending]");
    const cursor = prose.querySelector("[data-cursor]");

    if (props.isStreaming) {
      const { blocks, pendingHtml } = renderStreamingMarkdown(text, cache);
      reconcileSettled(blocksEl, blocks);
      pendingEl.innerHTML = pendingHtml;
      cursor.style.display = props.isReasoning ? "none" : "";
    } else {
      // Final / historical render: one settled block, no pending, no cursor.
      cache = createStreamingCache();
      blocksEl.innerHTML = `<div data-bid="final">${renderMarkdown(text)}</div>`;
      pendingEl.innerHTML = "";
      cursor.style.display = "none";
    }
  }

  function renderActions() {
    const host = root.querySelector("[data-actions]");
    host.style.display = props.isStreaming ? "none" : "";
    if (props.isStreaming) {
      host.innerHTML = "";
      return;
    }
    host.innerHTML = `
      ${props.onRegenerate ? `<button class="chat-action-btn" data-regen title="Regenerate response">${ICON_REGEN}</button>` : ""}
      <button class="chat-action-btn" data-copy title="${copied ? "Copied!" : "Copy to clipboard"}">${copied ? ICON_CHECK : ICON_COPY}</button>
      <button class="chat-action-btn ${showRaw ? "chat-action-on" : ""}" data-rawtoggle title="${showRaw ? "Show rendered" : "Show raw"}">${ICON_CODE}</button>`;
  }

  async function onCopy() {
    if (await copyText(getTextContent(props.content))) {
      copied = true;
      renderActions();
      setTimeout(() => {
        copied = false;
        renderActions();
      }, 2000);
    }
  }

  function updateAssistant() {
    renderReasoning();
    renderImages();
    renderProse();
    renderActions();
  }

  // ===== user / system =====
  function renderUser() {
    lastImagesKey = null;
    const text = getTextContent(props.content);
    const urls = getImageUrls(props.content);
    const canEdit = typeof props.onEdit === "function" && urls.length === 0;

    root.className = "chat-msg chat-msg-user";
    if (isEditing) {
      root.innerHTML = `
        <div class="chat-bubble chat-bubble-user">
          <div class="chat-edit">
            <textarea class="chat-edit-area" rows="3"></textarea>
            <div class="chat-edit-actions">
              <button class="chat-edit-btn" data-cancel title="Cancel">${ICON_X}</button>
              <button class="chat-edit-btn" data-save title="Save">${ICON_SAVE}</button>
            </div>
          </div>
        </div>`;
      const ta = root.querySelector(".chat-edit-area");
      ta.value = text;
      ta.focus();
      ta.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && !e.shiftKey) {
          e.preventDefault();
          saveEdit(ta.value);
        } else if (e.key === "Escape") {
          isEditing = false;
          renderUser();
        }
      });
      root.querySelector("[data-cancel]").addEventListener("click", () => {
        isEditing = false;
        renderUser();
      });
      root.querySelector("[data-save]").addEventListener("click", () => saveEdit(ta.value));
      return;
    }

    root.innerHTML = `
      <div class="chat-bubble chat-bubble-user">
        ${urls.length ? `<div class="chat-images chat-images-user">${imagesHtml(urls)}</div>` : ""}
        <div class="chat-user-text">${escapeHtml(text)}</div>
        ${canEdit ? `<button class="chat-edit-trigger" data-edit title="Edit message">${ICON_PENCIL}</button>` : ""}
      </div>`;
    root.querySelectorAll("[data-img]").forEach((btn) => {
      btn.addEventListener("click", () => openImageModal(urls[Number(btn.getAttribute("data-img"))]));
    });
    if (canEdit) {
      root.querySelector("[data-edit]").addEventListener("click", () => {
        isEditing = true;
        renderUser();
      });
    }
  }

  function saveEdit(value) {
    const trimmed = value.trim();
    isEditing = false;
    if (props.onEdit && trimmed !== getTextContent(props.content)) {
      props.onEdit(trimmed);
    } else {
      renderUser();
    }
  }

  // initial build
  if (props.role === "assistant") {
    buildAssistant();
    updateAssistant();
  } else {
    renderUser();
  }

  return {
    el: root,
    update(next) {
      props = { reasoning_content: "", reasoningTimeMs: 0, isStreaming: false, isReasoning: false, ...next };
      if (props.role === "assistant") updateAssistant();
      else renderUser();
    },
    destroy() {
      if (proseDisconnect) proseDisconnect();
      cleanupAll([]);
    },
  };
}
