// Capture dialog (request/response inspector), ported from CaptureDialog.svelte.
import { el, escapeHtml } from "../dom.js";

function decodeBody(body) {
  if (!body) return "";
  try {
    const binary = atob(body);
    const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return body;
  }
}

function formatJson(str) {
  try {
    return JSON.stringify(JSON.parse(str), null, 2);
  } catch {
    return str;
  }
}

function getContentType(headers) {
  if (!headers) return "";
  return (headers["Content-Type"] || headers["content-type"] || "").toLowerCase();
}
function isImageCT(ct) {
  return ct.startsWith("image/");
}
function isTextCT(ct) {
  return (
    ct.startsWith("text/") ||
    ct.includes("application/json") ||
    ct.includes("application/xml") ||
    ct.includes("application/javascript")
  );
}
function imageDataUrl(body, ct) {
  const mime = ct.split(";")[0].trim();
  return `data:${mime};base64,${body}`;
}

function parseSSEChat(text) {
  const out = { reasoning: "", content: "" };
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || !trimmed.startsWith("data: ")) continue;
    const data = trimmed.slice(6);
    if (data === "[DONE]") continue;
    try {
      const parsed = JSON.parse(data);
      const delta = parsed.choices?.[0]?.delta;
      if (delta?.content) out.content += delta.content;
      if (delta?.reasoning_content) out.reasoning += delta.reasoning_content;
      if (delta?.reasoning) out.reasoning += delta.reasoning;
    } catch {
      /* skip */
    }
  }
  return out;
}

// Build a headers table HTML fragment
function headersTable(headers) {
  const rows = Object.entries(headers || {})
    .map(
      ([k, v]) => `
        <tr>
          <td class="capture-h-key">${escapeHtml(k)}</td>
          <td class="capture-h-val">${escapeHtml(String(v))}</td>
        </tr>`
    )
    .join("");
  return `<table class="capture-headers"><tbody>${rows}</tbody></table>`;
}

// Build body tabs HTML
function bodyTabsHTML(active, options) {
  return options
    .map(
      (opt) =>
        `<button class="capture-tab ${opt === active ? "capture-tab-active" : ""}" data-tab="${opt}">${opt[0].toUpperCase() + opt.slice(1)}</button>`
    )
    .join("");
}

export function CaptureDialogController() {
  const dlg = el(`<dialog class="capture-dlg"></dialog>`);
  document.body.appendChild(dlg);

  let state = {
    capture: null,
    reqBodyTab: "pretty",
    respBodyTab: "pretty",
    copiedReq: false,
    copiedResp: false,
  };

  function close() {
    if (dlg.open) dlg.close();
  }

  function render() {
    const c = state.capture;
    if (!c) {
      dlg.innerHTML = `
        <div class="capture-empty">
          <p class="capture-empty-title">Capture not found</p>
          <p class="capture-empty-sub">The capture may have expired or was never recorded.</p>
          <button class="btn" data-close>Close</button>
        </div>`;
      dlg.querySelector("[data-close]").addEventListener("click", close);
      return;
    }

    const reqCt = getContentType(c.req_headers);
    const respCt = getContentType(c.resp_headers);
    const isReqJson = reqCt.includes("json");
    const isRespImage = isImageCT(respCt);
    const isRespText = isTextCT(respCt);
    const isRespJson = respCt.includes("json");
    const isSSE = respCt.includes("text/event-stream");

    const reqBodyRaw = decodeBody(c.req_body);
    const reqBodyPretty = isReqJson ? formatJson(reqBodyRaw) : reqBodyRaw;
    const reqDisplay = state.reqBodyTab === "pretty" ? reqBodyPretty : reqBodyRaw;

    const respBodyRaw = decodeBody(c.resp_body);
    const respBodyPretty = isRespJson ? formatJson(respBodyRaw) : respBodyRaw;
    const sseChat = isSSE && respBodyRaw ? parseSSEChat(respBodyRaw) : null;
    const respDisplay = state.respBodyTab === "pretty" ? respBodyPretty : respBodyRaw;

    // Build body section
    function reqBodySection() {
      if (!reqBodyRaw) {
        return `<pre class="capture-pre capture-pre-empty">(empty)</pre>`;
      }
      const tabsHTML = isReqJson
        ? `<div class="capture-tab-row">
             <div class="capture-tab-group">${bodyTabsHTML(state.reqBodyTab, ["pretty", "raw"])}</div>
             <button class="capture-tab" data-copy="req">${state.copiedReq ? "Copied!" : "Copy"}</button>
           </div>`
        : `<div class="capture-tab-row"><span></span><button class="capture-tab" data-copy="req">${state.copiedReq ? "Copied!" : "Copy"}</button></div>`;
      return `${tabsHTML}<pre class="capture-pre">${escapeHtml(reqDisplay)}</pre>`;
    }

    function respBodySection() {
      if (isRespImage && c.resp_body) {
        return `<div class="capture-image-wrap"><img src="${imageDataUrl(c.resp_body, respCt)}" alt="Response" /></div>`;
      }
      if (isSSE || isRespText) {
        const tabOptions = [];
        if (isSSE) tabOptions.push("chat");
        if (isRespJson) tabOptions.push("pretty");
        if (isSSE || isRespJson) tabOptions.push("raw");
        const tabsHTML =
          tabOptions.length > 0
            ? `<div class="capture-tab-row">
                 <div class="capture-tab-group">${bodyTabsHTML(state.respBodyTab, tabOptions)}</div>
                 <button class="capture-tab" data-copy="resp">${state.copiedResp ? "Copied!" : "Copy"}</button>
               </div>`
            : `<div class="capture-tab-row"><span></span><button class="capture-tab" data-copy="resp">${state.copiedResp ? "Copied!" : "Copy"}</button></div>`;

        let body;
        if (state.respBodyTab === "chat" && sseChat) {
          body = `<div class="capture-chat">`;
          if (sseChat.reasoning) {
            body += `<div class="capture-chat-label">Reasoning</div><pre class="capture-pre capture-pre-reasoning">${escapeHtml(sseChat.reasoning)}</pre>`;
            if (sseChat.content) body += `<div class="capture-chat-label">Response</div>`;
          }
          if (sseChat.content) body += `<pre class="capture-pre">${escapeHtml(sseChat.content)}</pre>`;
          if (!sseChat.reasoning && !sseChat.content) body += `<pre class="capture-pre">(empty)</pre>`;
          body += `</div>`;
        } else {
          body = `<pre class="capture-pre">${escapeHtml(respDisplay || "(empty)")}</pre>`;
        }
        return tabsHTML + body;
      }
      if (respBodyRaw) {
        return `<div class="capture-binary">(binary data - ${escapeHtml(respCt || "unknown content type")})</div>`;
      }
      return `<pre class="capture-pre">(empty)</pre>`;
    }

    dlg.innerHTML = `
      <div class="capture-shell">
        <div class="capture-head">
          <h2>Capture #${c.id + 1}${c.req_path ? ` <span class="capture-path">${escapeHtml(c.req_path)}</span>` : ""}</h2>
          <button class="capture-close" data-close>&times;</button>
        </div>
        <div class="capture-body">
          <details open>
            <summary class="capture-summary">Request Headers</summary>
            <div class="capture-scroll capture-scroll-short">${headersTable(c.req_headers)}</div>
          </details>
          <details open>
            <summary class="capture-summary">Request Body</summary>
            ${reqBodySection()}
          </details>
          <details open>
            <summary class="capture-summary">Response Headers</summary>
            <div class="capture-scroll capture-scroll-short">${headersTable(c.resp_headers)}</div>
          </details>
          <details open>
            <summary class="capture-summary">Response Body</summary>
            ${respBodySection()}
          </details>
        </div>
        <div class="capture-foot">
          <button class="btn" data-close>Close</button>
        </div>
      </div>
    `;

    dlg.querySelector("[data-close]")?.addEventListener("click", close);
    dlg.querySelectorAll("[data-tab]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const tab = btn.getAttribute("data-tab");
        // Determine if this is req or resp based on which section's tabs we're in
        // by checking parent context.
        const section = btn.closest("details");
        const summary = section?.querySelector(".capture-summary")?.textContent || "";
        if (summary.includes("Request")) state.reqBodyTab = tab;
        else state.respBodyTab = tab;
        render();
      });
    });
    dlg.querySelectorAll("[data-copy]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const which = btn.getAttribute("data-copy");
        let text;
        if (which === "req") text = reqDisplay;
        else {
          if (state.respBodyTab === "chat" && sseChat) {
            text = (sseChat.reasoning ? sseChat.reasoning + "\n\n" : "") + sseChat.content;
          } else {
            text = respDisplay;
          }
        }
        try {
          await navigator.clipboard.writeText(text);
          if (which === "req") {
            state.copiedReq = true;
            setTimeout(() => {
              state.copiedReq = false;
              render();
            }, 1500);
          } else {
            state.copiedResp = true;
            setTimeout(() => {
              state.copiedResp = false;
              render();
            }, 1500);
          }
          render();
        } catch {
          /* ignore */
        }
      });
    });
  }

  function open(capture) {
    state.capture = capture;
    // Reset tabs based on content types
    if (capture) {
      const reqCt = getContentType(capture.req_headers);
      const respCt = getContentType(capture.resp_headers);
      state.reqBodyTab = reqCt.includes("json") ? "pretty" : "raw";
      state.respBodyTab = respCt.includes("text/event-stream")
        ? "chat"
        : respCt.includes("json")
        ? "pretty"
        : "raw";
      state.copiedReq = false;
      state.copiedResp = false;
    }
    render();
    if (!dlg.open) dlg.showModal();
  }

  dlg.addEventListener("close", () => {
    state.capture = null;
  });

  // Close on Escape is automatic for <dialog>; clicking backdrop closes too:
  dlg.addEventListener("click", (e) => {
    if (e.target === dlg) close();
  });

  return { open, close, destroy() {
    dlg.remove();
  }};
}
