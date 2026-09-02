// Audio transcription interface: drag-drop file picker + transcription result.
// Ported from components/playground/AudioInterface.svelte.
import { el, cleanupAll, escapeHtml } from "../dom.js";
import { models } from "../api.js";
import { playgroundStores } from "../playgroundActivity.js";
import { persistent } from "../store.js";
import { ModelSelector } from "./modelSelector.js";
import { transcribeAudio } from "../api/audio.js";

const ACCEPTED_FORMATS = [".mp3", ".wav", ".ogg"];
const MAX_FILE_SIZE = 25 * 1024 * 1024;

function formatFileSize(b) {
  if (b < 1024) return b + " B";
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + " KB";
  return (b / (1024 * 1024)).toFixed(1) + " MB";
}

export function AudioInterface() {
  const selectedModel = persistent("playground-audio-model", "");

  let selectedFile = null;
  let isTranscribing = false;
  let transcriptionResult = null;
  let error = null;
  let abortController = null;
  let isDragging = false;
  let copied = false;

  const root = el(`
    <div class="pg-audio">
      <div class="pg-audio-toolbar" data-toolbar></div>
      <div class="pg-audio-stage" data-stage></div>
      <div class="pg-audio-actions" data-actions></div>
    </div>
  `);

  const toolbar = root.querySelector("[data-toolbar]");
  const stage = root.querySelector("[data-stage]");
  const actions = root.querySelector("[data-actions]");

  const modelSel = ModelSelector({ value: selectedModel, placeholder: "Select an audio model..." });
  toolbar.appendChild(modelSel.el);

  // hidden file input
  const fileInput = el(`<input type="file" accept=".mp3,.wav,.ogg" style="display:none">`);
  document.body.appendChild(fileInput);

  function validateFile(file) {
    const ext = "." + (file.name.split(".").pop() || "").toLowerCase();
    if (!ACCEPTED_FORMATS.includes(ext)) return { valid: false, error: "Invalid file type. Accepted: MP3, WAV, OGG" };
    if (file.size > MAX_FILE_SIZE) return { valid: false, error: "File too large. Maximum: 25MB" };
    return { valid: true };
  }

  fileInput.addEventListener("change", (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    const v = validateFile(f);
    if (v.valid) { selectedFile = f; error = null; transcriptionResult = null; }
    else { error = v.error; selectedFile = null; }
    renderStage();
    renderActions();
  });

  function renderStage() {
    if (isTranscribing) {
      stage.innerHTML = `
        <div class="pg-audio-msg">
          <div class="spinner"></div>
          <p>Transcribing audio...</p>
        </div>`;
      return;
    }
    if (error) {
      stage.innerHTML = `
        <div class="pg-audio-msg pg-error">
          <p class="pg-error-title">Error</p>
          <p class="pg-error-body">${escapeHtml(error)}</p>
        </div>`;
      return;
    }
    if (transcriptionResult) {
      stage.innerHTML = `
        <div class="pg-audio-result">
          <div class="pg-audio-result-head">
            <h3>Transcription Result</h3>
            <button class="btn btn--sm" data-copy title="${copied ? "Copied!" : "Copy to clipboard"}">
              ${copied
                ? `<svg class="icon-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" style="color: var(--color-success)"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"></path></svg>`
                : `<svg class="icon-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"></path></svg>`}
            </button>
          </div>
          <div class="pg-audio-result-body">${escapeHtml(transcriptionResult)}</div>
        </div>`;
      return;
    }
    if (selectedFile) {
      stage.innerHTML = `
        <div class="pg-audio-msg">
          <p class="pg-audio-file-name">${escapeHtml(selectedFile.name)}</p>
          <p class="pg-audio-file-size">${formatFileSize(selectedFile.size)}</p>
        </div>`;
      return;
    }
    stage.innerHTML = `
      <div class="pg-audio-dropzone ${isDragging ? "pg-dropzone-active" : ""}" data-drop>
        <p>Drag and drop an audio file here</p>
        <p class="pg-audio-dropzone-sub">or use the Browse button below</p>
        <p class="pg-audio-dropzone-sub">Accepted formats: MP3, WAV, OGG (max 25MB)</p>
      </div>`;
    const dz = stage.querySelector("[data-drop]");
    if (dz) {
      dz.addEventListener("dragover", (e) => { e.preventDefault(); isDragging = true; dz.classList.add("pg-dropzone-active"); });
      dz.addEventListener("dragleave", () => { isDragging = false; dz.classList.remove("pg-dropzone-active"); });
      dz.addEventListener("drop", (e) => {
        e.preventDefault();
        isDragging = false;
        dz.classList.remove("pg-dropzone-active");
        const f = e.dataTransfer?.files?.[0];
        if (!f) return;
        const v = validateFile(f);
        if (v.valid) { selectedFile = f; error = null; transcriptionResult = null; }
        else { error = v.error; selectedFile = null; }
        renderStage();
        renderActions();
      });
    }
  }

  stage.addEventListener("click", (e) => {
    if (e.target.closest("[data-copy]")) {
      if (transcriptionResult) {
        navigator.clipboard.writeText(transcriptionResult);
        copied = true;
        renderStage();
        setTimeout(() => { copied = false; renderStage(); }, 2000);
      }
    }
  });

  function canTranscribe() { return selectedFile !== null && selectedModel.get() !== "" && !isTranscribing; }

  function renderActions() {
    if (isTranscribing) {
      actions.innerHTML = `
        <button class="btn" data-browse disabled>Browse Files</button>
        <span class="pg-flex-grow"></span>
        <button class="btn pg-btn-cancel" data-cancel>Cancel</button>`;
      return;
    }
    actions.innerHTML = `
      <button class="btn" data-browse>Browse Files</button>
      <span class="pg-flex-grow"></span>
      <button class="btn btn--primary" data-transcribe ${canTranscribe() ? "" : "disabled"}>Transcribe</button>
      <button class="btn" data-clear ${!selectedFile && !transcriptionResult && !error ? "disabled" : ""}>Clear</button>`;
  }

  actions.addEventListener("click", (e) => {
    if (e.target.closest("[data-browse]")) fileInput.click();
    else if (e.target.closest("[data-cancel]")) abortController?.abort();
    else if (e.target.closest("[data-transcribe]")) transcribe();
    else if (e.target.closest("[data-clear]")) {
      selectedFile = null; transcriptionResult = null; error = null;
      fileInput.value = "";
      renderStage(); renderActions();
    }
  });

  async function transcribe() {
    if (!selectedFile || !selectedModel.get() || isTranscribing) return;
    isTranscribing = true;
    error = null;
    transcriptionResult = null;
    abortController = new AbortController();
    playgroundStores.audioTranscribing.set(true);
    renderStage();
    renderActions();
    try {
      const resp = await transcribeAudio(selectedModel.get(), selectedFile, abortController.signal);
      transcriptionResult = resp.text;
    } catch (err) {
      if (err.name !== "AbortError") error = err.message || "An error occurred";
    } finally {
      isTranscribing = false;
      abortController = null;
      playgroundStores.audioTranscribing.set(false);
      renderStage();
      renderActions();
    }
  }

  const subs = [
    models.subscribe(() => {
      const has = models.get().some((m) => !m.unlisted);
      root.classList.toggle("pg-no-models", !has);
    }),
    selectedModel.subscribe(() => {
      const b = actions.querySelector("[data-transcribe]");
      if (b) b.disabled = !canTranscribe();
    }),
  ];

  renderStage();
  renderActions();

  return {
    el: root,
    destroy() {
      fileInput.remove();
      cleanupAll([modelSel.destroy, ...subs]);
    },
  };
}
