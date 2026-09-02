// Image generation tab: OpenAI / SDAPI modes, persistent settings, LoRA support.
// Ported from components/playground/ImageInterface.svelte.
import { el, cleanupAll } from "../dom.js";
import { models } from "../api.js";
import { playgroundStores } from "../playgroundActivity.js";
import { persistent } from "../store.js";
import { ModelSelector } from "./modelSelector.js";
import { ExpandableTextarea } from "./expandableTextarea.js";
import { generateImage } from "../api/image.js";
import { generateSdImage, fetchSdLoras } from "../api/sd.js";

const SIZE_OPTIONS = `
  <optgroup label="Square">
    <option value="512x512">512x512</option>
    <option value="1024x1024">1024x1024</option>
  </optgroup>
  <optgroup label="Landscape">
    <option value="1024x768">1024x768 (4:3)</option>
    <option value="1280x720">1280x720 (16:9)</option>
    <option value="1792x1024">1792x1024 (SDXL)</option>
  </optgroup>
  <optgroup label="Portrait">
    <option value="768x1024">768x1024 (3:4)</option>
    <option value="720x1280">720x1280 (9:16)</option>
    <option value="1024x1792">1024x1792 (SDXL)</option>
  </optgroup>
`;

const SAMPLER_OPTIONS = ["", "euler_a", "euler", "heun", "dpm2", "dpmpp2s_a", "dpmpp2m", "dpmpp2mv2", "ipndm", "ipndm_v", "lcm", "ddim_trailing", "tcd"];
const SCHEDULER_OPTIONS = ["", "discrete", "karras", "exponential", "ays", "gits"];

export function ImageInterface() {
  const selectedModel = persistent("playground-image-model", "");
  const selectedSize = persistent("playground-image-size", "1024x1024");
  const apiMode = persistent("playground-image-api-mode", "openai");
  const sdNegativePrompt = persistent("playground-sdapi-negative-prompt", "");
  const sdSteps = persistent("playground-sdapi-steps", 20);
  const sdCfgScale = persistent("playground-sdapi-cfg-scale", 7);
  const sdSeed = persistent("playground-sdapi-seed", -1);
  const sdSampler = persistent("playground-sdapi-sampler", "");
  const sdScheduler = persistent("playground-sdapi-scheduler", "");
  const sdBatchSize = persistent("playground-sdapi-batch-size", 1);

  let prompt = "";
  let isGenerating = false;
  let generatedImages = [];
  let error = null;
  let abortController = null;
  let showFullscreen = false;
  let fullscreenIndex = 0;
  let showSettings = false;
  let availableLoras = [];
  let selectedLoras = [];
  let isLoadingLoras = false;
  let lorasLoaded = false;
  let loraError = null;

  const root = el(`
    <div class="pg-image">
      <div class="pg-image-toolbar" data-toolbar></div>
      <div class="pg-image-settings" data-settings style="display:none"></div>
      <div class="pg-image-stage" data-stage></div>
      <div class="pg-image-prompt-row" data-prompt-row></div>
    </div>
  `);

  const toolbarEl = root.querySelector("[data-toolbar]");
  const settingsEl = root.querySelector("[data-settings]");
  const stageEl = root.querySelector("[data-stage]");
  const promptRowEl = root.querySelector("[data-prompt-row]");

  // Model selector + API mode + size + (optional) Settings toggle
  const modelSel = ModelSelector({ value: selectedModel, placeholder: "Select an image model...", disabled: false });
  toolbarEl.appendChild(modelSel.el);

  const modeSel = el(`
    <select class="pg-input" data-mode>
      <option value="openai">OpenAI</option>
      <option value="sdapi">SDAPI</option>
    </select>
  `);
  modeSel.value = apiMode.get();
  modeSel.addEventListener("change", () => apiMode.set(modeSel.value));
  toolbarEl.appendChild(modeSel);

  const sizeSel = el(`<select class="pg-input" data-size>${SIZE_OPTIONS}</select>`);
  sizeSel.value = selectedSize.get();
  sizeSel.addEventListener("change", () => selectedSize.set(sizeSel.value));
  toolbarEl.appendChild(sizeSel);

  const settingsToggle = el(`<button class="pg-input pg-btn" data-toggle-settings>Settings</button>`);
  toolbarEl.appendChild(settingsToggle);

  function renderSettingsVisibility() {
    const isSdapi = apiMode.get() === "sdapi";
    settingsToggle.style.display = isSdapi ? "" : "none";
    settingsEl.style.display = isSdapi && showSettings ? "" : "none";
    settingsToggle.textContent = showSettings ? "Hide Settings" : "Settings";
  }

  function renderSettings() {
    const isSdapi = apiMode.get() === "sdapi";
    if (!isSdapi || !showSettings) return;
    settingsEl.innerHTML = `
      <div class="pg-sd-grid">
        <label class="pg-sd-label">Steps<input type="number" class="pg-input" data-k="steps" min="1" max="150" value="${sdSteps.get()}"></label>
        <label class="pg-sd-label">CFG Scale<input type="number" class="pg-input" data-k="cfg" min="1" max="30" step="0.5" value="${sdCfgScale.get()}"></label>
        <label class="pg-sd-label">Seed (-1 = random)<input type="number" class="pg-input" data-k="seed" min="-1" value="${sdSeed.get()}"></label>
        <label class="pg-sd-label">Batch Size<input type="number" class="pg-input" data-k="batch" min="1" max="8" value="${sdBatchSize.get()}"></label>
        <label class="pg-sd-label">Sampler
          <select class="pg-input" data-k="sampler">
            ${SAMPLER_OPTIONS.map((s) => `<option value="${s}" ${s === sdSampler.get() ? "selected" : ""}>${s || "Default"}</option>`).join("")}
          </select>
        </label>
        <label class="pg-sd-label">Scheduler
          <select class="pg-input" data-k="scheduler">
            ${SCHEDULER_OPTIONS.map((s) => `<option value="${s}" ${s === sdScheduler.get() ? "selected" : ""}>${s || "Auto for model"}</option>`).join("")}
          </select>
        </label>
      </div>
      <label class="pg-sd-label pg-sd-neg">Negative Prompt
        <textarea class="pg-input" data-k="neg" rows="2" placeholder="Elements to avoid...">${escapeHtml(sdNegativePrompt.get())}</textarea>
      </label>
      <div class="pg-sd-loras">
        <span class="pg-sd-loras-label">LoRAs</span>
        <div class="pg-sd-loras-actions">
          <button class="pg-input pg-btn" data-load-loras ${!selectedModel.get() || isLoadingLoras ? "disabled" : ""}>
            ${isLoadingLoras ? "Loading..." : lorasLoaded ? "Reload LoRAs" : "Load LoRAs"}
          </button>
          ${lorasLoaded && availableLoras.length > 0 ? `
            <select class="pg-input pg-sd-loras-select" data-add-lora>
              <option value="">Add a LoRA...</option>
              ${availableLoras
                .filter((l) => !selectedLoras.some((s) => s.path === l.path))
                .map((l) => `<option value="${escapeAttr(l.path)}">${escapeText(l.name)}</option>`)
                .join("")}
            </select>` : ""}
        </div>
        ${loraError ? `<p class="pg-sd-lora-err">${escapeText(loraError)}</p>` : ""}
        ${lorasLoaded && availableLoras.length === 0 ? `<p class="muted pg-sd-lora-empty">No LoRAs available</p>` : ""}
        <div class="pg-sd-lora-list">
          ${selectedLoras
            .map(
              (l, i) => `
                <div class="pg-sd-lora-item">
                  <span class="pg-sd-lora-name">${escapeText(getLoraName(l.path))}</span>
                  <input type="number" class="pg-input pg-sd-lora-mult" data-mult-i="${i}" value="${l.multiplier}" min="0" max="2" step="0.1">
                  <button class="pg-sd-lora-rm" data-rm-i="${i}" aria-label="Remove LoRA">x</button>
                </div>`
            )
            .join("")}
        </div>
      </div>
    `;
  }

  function escapeAttr(s) { return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;"); }
  function escapeText(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }
  function getLoraName(path) { return availableLoras.find((l) => l.path === path)?.name ?? path; }

  settingsEl.addEventListener("input", (e) => {
    const t = e.target;
    const k = t.getAttribute("data-k");
    if (!k) return;
    const v = t.type === "number" ? Number(t.value) : t.value;
    if (k === "steps") sdSteps.set(v);
    else if (k === "cfg") sdCfgScale.set(v);
    else if (k === "seed") sdSeed.set(v);
    else if (k === "batch") sdBatchSize.set(v);
    else if (k === "sampler") sdSampler.set(v);
    else if (k === "scheduler") sdScheduler.set(v);
    else if (k === "neg") sdNegativePrompt.set(v);
  });
  settingsEl.addEventListener("change", (e) => {
    const addSel = e.target.closest("[data-add-lora]");
    if (addSel && addSel.value) {
      const path = addSel.value;
      const lora = availableLoras.find((l) => l.path === path);
      if (lora && !selectedLoras.some((l) => l.path === path)) {
        selectedLoras = [...selectedLoras, { path: lora.path, multiplier: 1.0 }];
        renderSettings();
      }
      return;
    }
  });
  settingsEl.addEventListener("click", (e) => {
    const loadBtn = e.target.closest("[data-load-loras]");
    if (loadBtn) { loadLoras(); return; }
    const rmBtn = e.target.closest("[data-rm-i]");
    if (rmBtn) {
      const i = Number(rmBtn.getAttribute("data-rm-i"));
      selectedLoras = selectedLoras.filter((_, idx) => idx !== i);
      renderSettings();
      return;
    }
  });
  settingsEl.addEventListener("input", (e) => {
    const mult = e.target.closest("[data-mult-i]");
    if (mult) {
      const i = Number(mult.getAttribute("data-mult-i"));
      const v = parseFloat(mult.value) || 1;
      selectedLoras = selectedLoras.map((l, idx) => (idx === i ? { ...l, multiplier: v } : l));
    }
  });

  settingsToggle.addEventListener("click", () => {
    showSettings = !showSettings;
    renderSettingsVisibility();
    renderSettings();
  });

  async function loadLoras() {
    if (!selectedModel.get() || isLoadingLoras) return;
    isLoadingLoras = true;
    loraError = null;
    try {
      availableLoras = await fetchSdLoras(selectedModel.get());
      lorasLoaded = true;
    } catch (err) {
      availableLoras = [];
      loraError = err.message || "Failed to load LoRAs";
      lorasLoaded = false;
    } finally {
      isLoadingLoras = false;
      renderSettings();
    }
  }

  // ---- Stage (image / spinner / error) ----
  function renderStage() {
    if (isGenerating) {
      stageEl.innerHTML = `
        <div class="pg-image-msg">
          <div class="spinner"></div>
          <p>Generating image...</p>
        </div>`;
      return;
    }
    if (error) {
      stageEl.innerHTML = `
        <div class="pg-image-msg pg-error">
          <p class="pg-error-title">Error</p>
          <p class="pg-error-body">${escapeText(error)}</p>
        </div>`;
      return;
    }
    if (generatedImages.length > 1) {
      stageEl.innerHTML = `
        <div class="pg-image-grid">
          ${generatedImages
            .map(
              (img, i) => `
                <div class="pg-image-cell">
                  <button class="pg-image-btn" data-fs="${i}"><img src="${img}" alt="AI generated content ${i + 1}"></button>
                  <button class="pg-image-dl" data-dl="${i}" aria-label="Download image">
                    <svg class="icon-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path></svg>
                  </button>
                </div>`
            )
            .join("")}
        </div>`;
      return;
    }
    if (generatedImages.length === 1) {
      stageEl.innerHTML = `
        <div class="pg-image-single">
          <button class="pg-image-btn" data-fs="0"><img src="${generatedImages[0]}" alt="AI generated content"></button>
          <button class="pg-image-dl pg-image-dl-big" data-dl="0" aria-label="Download image">
            <svg class="icon-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path></svg>
          </button>
        </div>`;
      return;
    }
    stageEl.innerHTML = `<div class="pg-image-msg muted"><p>Enter a prompt below to generate an image</p></div>`;
  }

  stageEl.addEventListener("click", (e) => {
    const fs = e.target.closest("[data-fs]");
    if (fs) {
      openFullscreen(Number(fs.getAttribute("data-fs")));
      return;
    }
    const dl = e.target.closest("[data-dl]");
    if (dl) {
      e.stopPropagation();
      downloadImage(Number(dl.getAttribute("data-dl")));
    }
  });

  // ---- Prompt row ----
  const promptArea = ExpandableTextarea({
    value: {
      get: () => prompt,
      set: (v) => { prompt = v; updateGenerateBtn(); },
      subscribe: () => () => {},
    },
    placeholder: "Describe the image you want to generate...",
    rows: 3,
    onkeydown: (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        generate();
      }
    },
    disabled: false,
  });
  promptRowEl.appendChild(promptArea.el);

  const actionWrap = el(`<div class="pg-image-actions"></div>`);
  promptRowEl.appendChild(actionWrap);

  function renderActionBtns() {
    if (isGenerating) {
      actionWrap.innerHTML = `<button class="btn pg-btn-cancel" data-cancel>Cancel</button>`;
    } else {
      actionWrap.innerHTML = `
        <button class="btn btn--primary" data-gen>Generate</button>
        <button class="btn" data-clear>Clear</button>
      `;
    }
  }
  function updateGenerateBtn() {
    const btn = actionWrap.querySelector("[data-gen]");
    if (btn) btn.disabled = !prompt.trim() || !selectedModel.get();
    const clr = actionWrap.querySelector("[data-clear]");
    if (clr) clr.disabled = generatedImages.length === 0 && !error && !prompt.trim();
  }
  actionWrap.addEventListener("click", (e) => {
    if (e.target.closest("[data-gen]")) generate();
    else if (e.target.closest("[data-cancel]")) cancelGeneration();
    else if (e.target.closest("[data-clear]")) clearImage();
  });

  // ---- Generation logic ----
  async function generate() {
    const trimmed = prompt.trim();
    if (!trimmed || !selectedModel.get() || isGenerating) return;
    isGenerating = true;
    error = null;
    abortController = new AbortController();
    playgroundStores.imageGenerating.set(true);

    renderStage();
    renderActionBtns();

    try {
      if (apiMode.get() === "sdapi") {
        const [w, h] = selectedSize.get().split("x").map(Number);
        const req = {
          model: selectedModel.get(),
          prompt: trimmed,
          negative_prompt: sdNegativePrompt.get() || undefined,
          width: w, height: h,
          steps: sdSteps.get(),
          cfg_scale: sdCfgScale.get(),
          seed: sdSeed.get(),
          batch_size: sdBatchSize.get(),
          sampler_name: sdSampler.get() || undefined,
          scheduler: sdScheduler.get() || undefined,
          lora: selectedLoras.length > 0 ? selectedLoras : undefined,
        };
        const resp = await generateSdImage(req, abortController.signal);
        if (resp.images && resp.images.length > 0) {
          generatedImages = resp.images.map((img) => `data:image/png;base64,${img}`);
        }
      } else {
        const resp = await generateImage(selectedModel.get(), trimmed, selectedSize.get(), abortController.signal);
        if (resp.data && resp.data.length > 0) {
          const d = resp.data[0];
          if (d.b64_json) generatedImages = [`data:image/png;base64,${d.b64_json}`];
          else if (d.url) generatedImages = [d.url];
        }
      }
    } catch (err) {
      if (err.name !== "AbortError") error = err.message || "An error occurred";
    } finally {
      isGenerating = false;
      abortController = null;
      playgroundStores.imageGenerating.set(false);
      renderStage();
      renderActionBtns();
      updateGenerateBtn();
    }
  }

  function cancelGeneration() { abortController?.abort(); }
  function clearImage() {
    generatedImages = [];
    error = null;
    prompt = "";
    promptArea.el.querySelector("textarea").value = "";
    renderStage();
    renderActionBtns();
    updateGenerateBtn();
  }

  function downloadImage(i = 0) {
    const img = generatedImages[i];
    if (!img) return;
    const a = document.createElement("a");
    a.href = img;
    a.download = `generated-image-${Date.now()}-${i}.png`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  }

  function openFullscreen(i = 0) {
    fullscreenIndex = i;
    showFullscreen = true;
    const overlay = el(`
      <div class="pg-image-fs" data-fs-close>
        <button class="pg-image-fs-close" data-fs-close-btn>×</button>
        <img src="${generatedImages[i]}" alt="AI generated content">
      </div>
    `);
    overlay.addEventListener("click", (e) => {
      if (e.target === overlay || e.target.closest("[data-fs-close-btn]")) {
        overlay.remove();
        showFullscreen = false;
      }
    });
    document.body.appendChild(overlay);
  }

  // ---- Reactivity ----
  const subs = [
    apiMode.subscribe(() => {
      renderSettingsVisibility();
      renderSettings();
    }),
    models.subscribe(() => {
      const hasModels = models.get().some((m) => !m.unlisted);
      root.classList.toggle("pg-no-models", !hasModels);
      if (!hasModels) stageEl.innerHTML = `<div class="pg-image-msg muted"><p>No models configured. Add models to your configuration to generate images.</p></div>`;
    }),
    selectedModel.subscribe(() => {
      modelSel.setDisabled(isGenerating);
      updateGenerateBtn();
    }),
  ];

  renderSettingsVisibility();
  renderStage();
  renderActionBtns();
  updateGenerateBtn();

  return {
    el: root,
    destroy() {
      cleanupAll([modelSel.destroy, promptArea.destroy, ...subs]);
    },
  };
}
