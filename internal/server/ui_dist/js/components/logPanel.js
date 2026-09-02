// Log viewer pane with persistent font-size / wrap / filter settings.
// Ported from components/LogPanel.svelte.
import { el, cleanupAll } from "../dom.js";
import { persistent } from "../store.js";

const FONT_SIZES = ["xxs", "xs", "small", "normal"];

export function LogPanel({ id, title, logData }) {
  // Per-panel persistent settings. id captured at init (must be stable for the panel's life).
  const fontSize = persistent(`logPanel-${id}-fontSize`, "normal");
  const wrapText = persistent(`logPanel-${id}-wrapText`, false);
  const showFilter = persistent(`logPanel-${id}-showFilter`, false);

  let filterRegex = "";
  let userScrolledUp = false;

  const root = el(`
    <div class="logpanel">
      <div class="logpanel-head">
        <div class="logpanel-head-row">
          <h3 class="logpanel-title">${title}</h3>
          <div class="logpanel-actions">
            <button class="logpanel-btn" data-act="font" title="Change font size">
              <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-4"><path d="M2 4v3h5v12h3V7h5V4H2zm19 5h-9v3h3v7h3v-7h3V9z"/></svg>
            </button>
            <button class="logpanel-btn" data-act="wrap" title="Toggle text wrap">
              <span class="wrap-icon"></span>
            </button>
            <button class="logpanel-btn" data-act="filter" title="Toggle filter">
              <span class="filter-icon"></span>
            </button>
          </div>
        </div>
        <div class="logpanel-filter" data-filter-row style="display:none">
          <input type="text" class="logpanel-filter-input" placeholder="Filter logs (regex)..." />
          <button class="logpanel-filter-clear" aria-label="Clear filter">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-6"><path fill-rule="evenodd" d="M12 2.25c-5.385 0-9.75 4.365-9.75 9.75s4.365 9.75 9.75 9.75 9.75-4.365 9.75-9.75S17.385 2.25 12 2.25Zm-1.72 6.97a.75.75 0 1 0-1.06 1.06L10.94 12l-1.72 1.72a.75.75 0 1 0 1.06 1.06L12 13.06l1.72 1.72a.75.75 0 1 0 1.06-1.06L13.06 12l1.72-1.72a.75.75 0 1 0-1.06-1.06L12 10.94l-1.72-1.72Z" clip-rule="evenodd"/></svg>
          </button>
        </div>
      </div>
      <div class="logpanel-body">
        <pre class="logpanel-pre"></pre>
      </div>
    </div>
  `);

  const filterRow = root.querySelector("[data-filter-row]");
  const filterInput = root.querySelector(".logpanel-filter-input");
  const filterClear = root.querySelector(".logpanel-filter-clear");
  const pre = root.querySelector(".logpanel-pre");

  function getFiltered() {
    if (!filterRegex) return logData.get();
    try {
      const re = new RegExp(filterRegex, "i");
      return logData
        .get()
        .split("\n")
        .filter((line) => re.test(line))
        .join("\n");
    } catch {
      return logData.get();
    }
  }

  function applyContent() {
    pre.textContent = getFiltered();
    if (!userScrolledUp) pre.scrollTop = pre.scrollHeight;
  }

  function applyClasses() {
    pre.className = "logpanel-pre";
    pre.classList.add(`font-${fontSize.get()}`);
    pre.classList.toggle("wrap-on", !!wrapText.get());
  }

  function applyFilterVisible() {
    filterRow.style.display = showFilter.get() ? "" : "none";
    if (!showFilter.get()) {
      filterRegex = "";
      applyContent();
    }
  }

  function applyWrapIcon() {
    const btn = root.querySelector('[data-act="wrap"] .wrap-icon');
    btn.innerHTML = wrapText.get()
      ? `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-4"><path fill-rule="evenodd" d="M3 6.75A.75.75 0 0 1 3.75 6h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 6.75ZM3 12a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 12Zm0 5.25a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75a.75.75 0 0 1-.75-.75Z" clip-rule="evenodd"/></svg>`
      : `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-4"><path fill-rule="evenodd" d="M3 6.75A.75.75 0 0 1 3.75 6h16.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 6.75ZM3 12a.75.75 0 0 1 .75-.75h10.5a.75.75 0 0 1 0 1.5H3.75A.75.75 0 0 1 3 12Zm0 5.25a.75.75 0 0 1 .75-.75h16.5a.75.75 0 0 1 0 1.5H3.75a.75.75 0 0 1-.75-.75Z" clip-rule="evenodd"/></svg>`;
  }

  function applyFilterIcon() {
    const btn = root.querySelector('[data-act="filter"] .filter-icon');
    btn.innerHTML = showFilter.get()
      ? `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" class="icon-4"><path fill-rule="evenodd" d="M10.5 3.75a6.75 6.75 0 1 0 0 13.5 6.75 6.75 0 0 0 0-13.5ZM2.25 10.5a8.25 8.25 0 1 1 14.59 5.28l4.69 4.69a.75.75 0 1 1-1.06 1.06l-4.69-4.69A8.25 8.25 0 0 1 2.25 10.5Z" clip-rule="evenodd"/></svg>`
      : `<svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="icon-4"><path stroke-linecap="round" stroke-linejoin="round" d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z"/></svg>`;
  }

  // initial paint
  applyClasses();
  applyWrapIcon();
  applyFilterIcon();
  applyFilterVisible();
  applyContent();

  // wire up controls
  root.querySelector('[data-act="font"]').addEventListener("click", () => {
    fontSize.update((prev) => {
      const i = FONT_SIZES.indexOf(prev);
      return FONT_SIZES[(i + 1) % FONT_SIZES.length];
    });
  });
  root.querySelector('[data-act="wrap"]').addEventListener("click", () => wrapText.update((v) => !v));
  root.querySelector('[data-act="filter"]').addEventListener("click", () =>
    showFilter.update((v) => !v)
  );
  filterInput.addEventListener("input", (e) => {
    filterRegex = e.target.value;
    applyContent();
  });
  filterClear.addEventListener("click", () => {
    filterInput.value = "";
    filterRegex = "";
    applyContent();
  });
  pre.addEventListener("scroll", () => {
    const { scrollTop, scrollHeight, clientHeight } = pre;
    userScrolledUp = scrollHeight - scrollTop - clientHeight > 40;
  });

  const subs = [
    logData.subscribe(applyContent),
    fontSize.subscribe(applyClasses),
    wrapText.subscribe(() => {
      applyClasses();
      applyWrapIcon();
    }),
    showFilter.subscribe(() => {
      applyFilterVisible();
      applyFilterIcon();
    }),
  ];

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
    },
  };
}
