// Playground page: tab shell with persistent tab selection (chat/images/speech/audio/rerank).
// Ported from routes/Playground.svelte. All tabs are kept mounted; non-active tabs are
// hidden via display:none to preserve state across tab switches (matching Svelte).
import { el, cleanupAll } from "../dom.js";
import { persistent } from "../store.js";
import { ChatInterface } from "../components/chatInterface.js";
import { ImageInterface } from "../components/imageInterface.js";
import { SpeechInterface } from "../components/speechInterface.js";
import { AudioInterface } from "../components/audioInterface.js";
import { RerankInterface } from "../components/rerankInterface.js";

const TABS = [
  { id: "chat", label: "Chat", factory: () => ChatInterface() },
  { id: "images", label: "Images", factory: () => ImageInterface() },
  { id: "speech", label: "Speech", factory: () => SpeechInterface() },
  { id: "audio", label: "Transcription", factory: () => AudioInterface() },
  { id: "rerank", label: "Rerank", factory: () => RerankInterface() },
];

export function PlaygroundPage() {
  const selectedTab = persistent("playground-selected-tab", "chat");
  let mobileMenuOpen = false;

  const root = el(`
    <div class="card pg-shell">
      <div class="pg-tab-bar">
        <div class="pg-tab-mobile" data-mobile-wrap>
          <button class="pg-tab-mobile-btn" data-mobile-btn>
            <span data-mobile-label></span>
            <svg class="icon-5 pg-tab-mobile-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path></svg>
          </button>
          <div class="pg-tab-mobile-pop" data-mobile-pop style="display:none"></div>
        </div>
        <div class="pg-tab-desktop" data-desktop></div>
      </div>
      <div class="pg-tab-content" data-content></div>
    </div>
  `);

  const mobileBtn = root.querySelector("[data-mobile-btn]");
  const mobileLabel = root.querySelector("[data-mobile-label]");
  const mobilePop = root.querySelector("[data-mobile-pop]");
  const desktopBar = root.querySelector("[data-desktop]");
  const content = root.querySelector("[data-content]");

  // Mount all five tabs once (display:none on non-active)
  const tabInstances = TABS.map((t) => ({ ...t, instance: t.factory() }));
  tabInstances.forEach((t) => {
    const wrap = document.createElement("div");
    wrap.className = "pg-tab-pane";
    wrap.style.display = t.id === selectedTab.get() ? "" : "none";
    wrap.appendChild(t.instance.el);
    content.appendChild(wrap);
    t.paneEl = wrap;
  });

  function currentLabel() {
    return TABS.find((t) => t.id === selectedTab.get())?.label || "";
  }

  function renderDesktop() {
    desktopBar.innerHTML = TABS.map(
      (t) =>
        `<button class="pg-tab-btn ${t.id === selectedTab.get() ? "pg-tab-active" : ""}" data-tab="${t.id}">${t.label}</button>`
    ).join("");
  }
  function renderMobile() {
    mobileLabel.textContent = currentLabel();
    if (!mobileMenuOpen) {
      mobilePop.style.display = "none";
      return;
    }
    mobilePop.innerHTML = TABS.map(
      (t) =>
        `<button class="pg-tab-mobile-item ${t.id === selectedTab.get() ? "pg-tab-mobile-active" : ""}" data-tab="${t.id}">${t.label}</button>`
    ).join("");
    mobilePop.style.display = "";
  }

  function selectTab(id) {
    selectedTab.set(id);
    mobileMenuOpen = false;
    tabInstances.forEach((t) => {
      t.paneEl.style.display = t.id === id ? "" : "none";
    });
    renderDesktop();
    renderMobile();
  }

  desktopBar.addEventListener("click", (e) => {
    const b = e.target.closest("[data-tab]");
    if (b) selectTab(b.getAttribute("data-tab"));
  });
  mobileBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    mobileMenuOpen = !mobileMenuOpen;
    renderMobile();
  });
  mobilePop.addEventListener("click", (e) => {
    const b = e.target.closest("[data-tab]");
    if (b) selectTab(b.getAttribute("data-tab"));
  });
  document.addEventListener("click", (e) => {
    if (!mobileMenuOpen) return;
    const wrap = root.querySelector("[data-mobile-wrap]");
    if (!wrap.contains(e.target)) {
      mobileMenuOpen = false;
      renderMobile();
    }
  });

  renderDesktop();
  renderMobile();

  return {
    el: root,
    destroy() {
      cleanupAll(tabInstances.map((t) => t.instance.destroy));
    },
  };
}
