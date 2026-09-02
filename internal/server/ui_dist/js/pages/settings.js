// Settings page: theme mode, models-page options, and build information.
// Ported from routes/Settings.svelte (accent themes omitted: this UI's theme
// system has light/dark/system modes only).
import { el, cleanupAll } from "../dom.js";
import { themeMode, connectionState } from "../theme.js";
import { versionInfo } from "../api.js";
import { persistent } from "../store.js";

const MODES = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "System" },
];

export function SettingsPage() {
  const showCapabilityTags = persistent("showCapabilityTags", true);

  const root = el(`
    <div class="page page-settings">
      <div class="page-heading"><h2 class="page-title">Settings</h2></div>

      <div class="card settings-card">
        <h3 class="settings-card-title">Appearance</h3>
        <div class="settings-row">
          <span class="settings-label">Theme</span>
          <div class="settings-segmented" data-mode-seg></div>
        </div>
      </div>

      <div class="card settings-card">
        <h3 class="settings-card-title">Models page</h3>
        <div class="settings-row">
          <div class="settings-row-text">
            <span class="settings-label">Show capability tags</span>
            <p class="muted settings-hint">Show all capability badges next to each model, mirroring the
            Details tab (vision, tools, context window, and more).</p>
          </div>
          <label class="settings-switch">
            <input type="checkbox" data-caps-toggle />
            <span class="settings-switch-slider"></span>
          </label>
        </div>
      </div>

      <div class="card settings-card">
        <h3 class="settings-card-title">Build Information</h3>
        <dl class="hw-dl" data-build-dl></dl>
      </div>
    </div>
  `);

  const modeSeg = root.querySelector("[data-mode-seg]");
  const capsToggle = root.querySelector("[data-caps-toggle]");
  const buildDl = root.querySelector("[data-build-dl]");

  function renderModes() {
    const current = themeMode.get();
    modeSeg.innerHTML = MODES.map(
      (m) =>
        `<button class="settings-seg-btn ${m.value === current ? "active" : ""}" data-mode="${m.value}">${m.label}</button>`
    ).join("");
  }

  modeSeg.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-mode]");
    if (!btn) return;
    themeMode.set(btn.getAttribute("data-mode"));
  });

  capsToggle.checked = showCapabilityTags.get();
  capsToggle.addEventListener("change", () => showCapabilityTags.set(capsToggle.checked));

  function renderBuild() {
    const conn = connectionState.get() ?? "unknown";
    const v = versionInfo.get() ?? {};
    buildDl.innerHTML = [
      ["Event Stream", conn],
      ["Version", v.version ?? "unknown"],
      ["Commit Hash", (v.commit ?? "unknown").substring(0, 7)],
      ["Build Date", v.build_date ?? "unknown"],
    ]
      .map(([k, val]) => `<dt class="hw-dt">${k}</dt><dd class="hw-dd">${String(val)}</dd>`)
      .join("");
  }

  const subs = [
    themeMode.subscribe(renderModes),
    connectionState.subscribe(renderBuild),
    versionInfo.subscribe(renderBuild),
  ];

  renderModes();
  renderBuild();

  return {
    el: root,
    destroy() {
      cleanupAll(subs);
    },
  };
}
