// TTS interface: model + voice selector, persistent autoplay, audio player.
// Ported from components/playground/SpeechInterface.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { models } from "../api.js";
import { playgroundStores } from "../playgroundActivity.js";
import { persistent } from "../store.js";
import { ModelSelector } from "./modelSelector.js";
import { ExpandableTextarea } from "./expandableTextarea.js";
import { generateSpeech } from "../api/speech.js";

const DEFAULT_VOICES = ["coral", "alloy", "echo", "fable", "onyx", "nova", "shimmer"];
const CACHE_KEY = "playground-speech-voices-cache";

function getVoicesCache() {
  try {
    const saved = localStorage.getItem(CACHE_KEY);
    return saved ? JSON.parse(saved) : {};
  } catch { return {}; }
}
function saveVoicesCache(c) {
  try { localStorage.setItem(CACHE_KEY, JSON.stringify(c)); } catch (e) { console.error(e); }
}

function formatTimestamp(d) {
  return d.toLocaleString(undefined, {
    month: "short", day: "numeric", hour: "numeric", minute: "2-digit", hour12: true,
  });
}

export function SpeechInterface() {
  const selectedModel = persistent("playground-speech-model", "");
  const selectedVoice = persistent("playground-speech-voice", "coral");
  const autoPlay = persistent("playground-speech-autoplay", false);

  let inputText = "";
  let isGenerating = false;
  let generatedAudioUrl = null;
  let generatedVoice = null;
  let generatedTimestamp = null;
  let error = null;
  let abortController = null;
  let availableVoices = [...DEFAULT_VOICES];
  let isLoadingVoices = false;
  let isInitialLoad = true;

  const root = el(`
    <div class="pg-speech">
      <div class="pg-speech-toolbar" data-toolbar></div>
      <div class="pg-speech-stage" data-stage></div>
      <div class="pg-speech-input" data-input></div>
    </div>
  `);

  const toolbar = root.querySelector("[data-toolbar]");
  const stageEl = root.querySelector("[data-stage]");
  const inputEl = root.querySelector("[data-input]");

  const modelSel = ModelSelector({ value: selectedModel, placeholder: "Select a speech model..." });
  toolbar.appendChild(modelSel.el);

  const voiceWrap = el(`<div class="pg-speech-voice-wrap"></div>`);
  toolbar.appendChild(voiceWrap);

  function renderVoices() {
    const cache = getVoicesCache();
    const showRefresh = selectedModel.get() && !cache[selectedModel.get()];
    voiceWrap.innerHTML = `
      <select class="pg-input" data-voice ${isGenerating || isLoadingVoices || !selectedModel.get() ? "disabled" : ""}>
        ${availableVoices.map((v) => `<option value="${v}" ${v === selectedVoice.get() ? "selected" : ""}>${escapeHtml(v)}</option>`).join("")}
        <option value="(refresh)">(refresh)</option>
      </select>
      ${showRefresh ? `
        <button class="btn pg-speech-refresh" data-refresh title="${isLoadingVoices ? "Loading voices..." : "Load voices for this model"}" ${isLoadingVoices ? "disabled" : ""}>
          ${isLoadingVoices ? `<span class="spinner spinner-sm"></span>` : `<svg class="icon-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg>`}
        </button>` : ""}
    `;
  }

  voiceWrap.addEventListener("change", (e) => {
    const sel = e.target.closest("[data-voice]");
    if (!sel) return;
    const v = sel.value;
    if (v === "(refresh)") refreshVoices();
    else selectedVoice.set(v);
  });
  voiceWrap.addEventListener("click", (e) => {
    if (e.target.closest("[data-refresh]")) refreshVoices();
  });

  // Initial load: restore cached voices for selected model
  if (isInitialLoad) {
    isInitialLoad = false;
    const cache = getVoicesCache();
    const m = selectedModel.get();
    if (m && cache[m]) availableVoices = cache[m];
  }

  async function refreshVoices() {
    const model = selectedModel.get();
    if (!model || isLoadingVoices) return;
    isLoadingVoices = true;
    renderVoices();
    try {
      const resp = await fetch(`/v1/audio/voices?model=${encodeURIComponent(model)}`);
      if (!resp.ok) throw new Error("fallback");
      const data = await resp.json();
      const voices = Array.isArray(data) ? data : (data.voices || DEFAULT_VOICES);
      availableVoices = voices.length > 0 ? voices : DEFAULT_VOICES;
      const cache = getVoicesCache();
      cache[model] = availableVoices;
      saveVoicesCache(cache);
      selectedVoice.set(availableVoices[0]);
    } catch {
      availableVoices = DEFAULT_VOICES;
      const cache = getVoicesCache();
      cache[model] = DEFAULT_VOICES;
      saveVoicesCache(cache);
      selectedVoice.set(DEFAULT_VOICES[0]);
    } finally {
      isLoadingVoices = false;
      renderVoices();
    }
  }

  function renderStage() {
    if (isGenerating) {
      stageEl.innerHTML = `
        <div class="pg-speech-msg">
          <div class="spinner"></div>
          <p>Generating speech...</p>
        </div>`;
      return;
    }
    if (error) {
      stageEl.innerHTML = `
        <div class="pg-speech-msg pg-error">
          <p class="pg-error-title">Error</p>
          <p class="pg-error-body">${escapeHtml(error)}</p>
        </div>`;
      return;
    }
    if (generatedAudioUrl) {
      stageEl.innerHTML = `
        <div class="pg-speech-player">
          <div class="pg-speech-meta">
            ${generatedVoice ? `<span>🎤 ${escapeHtml(generatedVoice)}</span>` : ""}
            ${generatedTimestamp ? `<span>🕒 ${escapeHtml(formatTimestamp(generatedTimestamp))}</span>` : ""}
            <button class="btn pg-speech-dl" data-dl title="Download audio file">
              <svg class="icon-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path></svg>
            </button>
          </div>
          <audio controls class="pg-speech-audio">
            <source src="${generatedAudioUrl}" type="audio/mpeg">
          </audio>
        </div>`;
      const a = stageEl.querySelector("audio");
      if (a && autoPlay.get()) {
        a.load();
        a.play().catch(() => {});
      }
      return;
    }
    stageEl.innerHTML = `
      <div class="pg-speech-msg muted">
        <svg class="icon-16" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"></path></svg>
        <p>Enter text below to convert to speech</p>
      </div>`;
  }

  stageEl.addEventListener("click", (e) => {
    if (e.target.closest("[data-dl]")) downloadAudio();
  });

  // ---- Input row ----
  const ta = ExpandableTextarea({
    value: {
      get: () => inputText,
      set: (v) => { inputText = v; updateGenState(); },
      subscribe: () => () => {},
    },
    placeholder: "Enter text to convert to speech...",
    rows: 8,
    onkeydown: (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); generate(); } },
  });
  inputEl.appendChild(ta.el);

  const actions = el(`<div class="pg-speech-actions"></div>`);
  inputEl.appendChild(actions);

  function renderActions() {
    if (isGenerating) {
      actions.innerHTML = `<button class="btn pg-btn-cancel" data-cancel>Cancel</button>`;
      return;
    }
    actions.innerHTML = `
      <button class="btn btn--primary" data-gen>Generate</button>
      <button class="btn" data-clear>Clear</button>
      <label class="pg-speech-autoplay">
        <input type="checkbox" data-autoplay ${autoPlay.get() ? "checked" : ""}>
        Auto-play
      </label>
    `;
  }
  function updateGenState() {
    const b = actions.querySelector("[data-gen]");
    if (b) b.disabled = !inputText.trim() || !selectedModel.get();
    const c = actions.querySelector("[data-clear]");
    if (c) c.disabled = !inputText.trim();
  }
  actions.addEventListener("click", (e) => {
    if (e.target.closest("[data-gen]")) generate();
    else if (e.target.closest("[data-cancel]")) abortController?.abort();
    else if (e.target.closest("[data-clear]")) { inputText = ""; ta.el.querySelector("textarea").value = ""; updateGenState(); }
  });
  actions.addEventListener("change", (e) => {
    if (e.target.hasAttribute("data-autoplay")) autoPlay.set(e.target.checked);
  });

  async function generate() {
    const trimmed = inputText.trim();
    if (!trimmed || !selectedModel.get() || isGenerating) return;
    isGenerating = true;
    error = null;
    abortController = new AbortController();
    playgroundStores.speechGenerating.set(true);

    renderStage();
    renderActions();

    try {
      const blob = await generateSpeech(selectedModel.get(), trimmed, selectedVoice.get(), abortController.signal);
      if (generatedAudioUrl) URL.revokeObjectURL(generatedAudioUrl);
      generatedAudioUrl = URL.createObjectURL(blob);
      generatedVoice = selectedVoice.get();
      generatedTimestamp = new Date();
    } catch (err) {
      if (err.name !== "AbortError") error = err.message || "An error occurred";
    } finally {
      isGenerating = false;
      abortController = null;
      playgroundStores.speechGenerating.set(false);
      renderStage();
      renderActions();
      updateGenState();
    }
  }

  function downloadAudio() {
    if (!generatedAudioUrl) return;
    const ts = (generatedTimestamp || new Date()).toISOString().replace(/[:.]/g, "-").slice(0, -5);
    const voice = generatedVoice || "speech";
    const a = document.createElement("a");
    a.href = generatedAudioUrl;
    a.download = `${voice}-${ts}.mp3`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  const subs = [
    models.subscribe(() => {
      const has = models.get().some((m) => !m.unlisted);
      root.classList.toggle("pg-no-models", !has);
    }),
    selectedModel.subscribe(() => {
      renderVoices();
      updateGenState();
    }),
    selectedVoice.subscribe(renderVoices),
    autoPlay.subscribe(() => {
      // re-render actions only if currently in idle state — no need otherwise
    }),
  ];

  renderVoices();
  renderStage();
  renderActions();
  updateGenState();

  return {
    el: root,
    destroy() {
      if (generatedAudioUrl) URL.revokeObjectURL(generatedAudioUrl);
      cleanupAll([modelSel.destroy, ta.destroy, ...subs]);
    },
  };
}
