// Hardware page: system/CPU/accelerator overview from /api/hardware, with a
// copyable plain-text summary. Ported from routes/Hardware.svelte.
import { el, escapeHtml } from "../dom.js";
import { getHardware } from "../api.js";
import { formatCapacity } from "../util/format.js";

function shown(value) {
  return value === null || value === undefined || value === "" ? "Not detected" : String(value);
}

function titleCase(value) {
  return value
    .replaceAll("_", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function osLabel(snapshot) {
  return [
    snapshot.operating_system.name ?? titleCase(snapshot.operating_system.family),
    snapshot.operating_system.version,
  ]
    .filter(Boolean)
    .join(" ");
}

function acceleratorTitle(accelerator) {
  return accelerator.model ?? `${titleCase(accelerator.kind)} ${accelerator.index + 1}`;
}

function environmentLabel(snapshot) {
  return (
    titleCase(snapshot.environment.kind) +
    (snapshot.environment.name ? ` (${snapshot.environment.name})` : "") +
    (snapshot.environment.version ? ` ${snapshot.environment.version}` : "")
  );
}

function driverLabel(accelerator) {
  return accelerator.driver
    ? [accelerator.driver.name, accelerator.driver.version].filter(Boolean).join(" ") || "Not detected"
    : "Not detected";
}

function acceleratorSummary(accelerator) {
  return [
    `Accelerator ${accelerator.index + 1}: ${acceleratorTitle(accelerator)}`,
    `  Type: ${titleCase(accelerator.kind)}`,
    `  Vendor: ${shown(accelerator.vendor)}`,
    `  Architecture: ${shown(accelerator.architecture)}`,
    `  Memory: ${accelerator.memory.capacity_bytes ? formatCapacity(accelerator.memory.capacity_bytes) : "Not detected"} (${titleCase(accelerator.memory.kind)})`,
    `  Driver: ${driverLabel(accelerator)}`,
    `  Power Limit: ${accelerator.power_limit_watts === null ? "Not detected" : `${accelerator.power_limit_watts} W`}`,
  ];
}

function hardwareSummary(snapshot) {
  const acceleratorLines =
    snapshot.accelerators.length === 0
      ? ["No accelerators were detected or exposed to this process."]
      : snapshot.accelerators.flatMap((accelerator, index) => [
          ...(index > 0 ? [""] : []),
          ...acceleratorSummary(accelerator),
        ]);

  return [
    "Hardware Summary",
    "",
    "System",
    `  Operating System: ${osLabel(snapshot)}`,
    `  Kernel: ${shown(snapshot.operating_system.kernel)}`,
    `  Architecture: ${snapshot.architecture.name}`,
    `  Environment: ${environmentLabel(snapshot)}`,
    `  System Memory: ${formatCapacity(snapshot.memory.capacity_bytes)}`,
    "",
    "CPU",
    `  Model: ${shown(snapshot.cpu.model)}`,
    `  Vendor: ${shown(snapshot.cpu.vendor)}`,
    `  Sockets: ${shown(snapshot.cpu.socket_count)}`,
    `  Physical Cores: ${shown(snapshot.cpu.physical_core_count)}`,
    `  Logical Threads: ${shown(snapshot.cpu.logical_thread_count)}`,
    "",
    `Accelerators (${snapshot.accelerators.length})`,
    ...acceleratorLines,
  ].join("\n");
}

function dl(pairs) {
  return `<dl class="hw-dl">${pairs
    .map(([k, v]) => `<dt class="hw-dt">${escapeHtml(k)}</dt><dd class="hw-dd">${escapeHtml(v)}</dd>`)
    .join("")}</dl>`;
}

export function HardwarePage() {
  const root = el(`
    <div class="page page-hardware">
      <div class="page-heading">
        <h2 class="page-title">Hardware</h2>
        <p class="muted page-subtitle">This is an experimental feature. Please share feedback in
          <a class="hw-issue-link" href="https://github.com/mostlygeek/llama-swap/issues/977" target="_blank" rel="noopener">issue 977</a>.</p>
      </div>
      <div data-body><div class="card hw-loading muted">Loading hardware profile…</div></div>
    </div>
  `);

  const bodyEl = root.querySelector("[data-body]");

  function renderOverview(hw) {
    const accelerators = hw.accelerators
      .map(
        (a) => `
        <article class="hw-accelerator">
          <div class="hw-accelerator-head">
            <h4 class="hw-accelerator-title">${escapeHtml(acceleratorTitle(a))}</h4>
            <p class="muted hw-accelerator-sub">${escapeHtml(shown(a.vendor))} · ${escapeHtml(titleCase(a.kind))}</p>
          </div>
          ${dl([
            ["Architecture", shown(a.architecture)],
            [
              "Memory",
              `${a.memory.capacity_bytes ? formatCapacity(a.memory.capacity_bytes) : "Not detected"} (${titleCase(a.memory.kind)})`,
            ],
            ["Driver", driverLabel(a)],
            ["Power Limit", a.power_limit_watts === null ? "Not detected" : `${a.power_limit_watts} W`],
          ])}
        </article>`
      )
      .join("");

    bodyEl.innerHTML = `
      <div class="hw-grid">
        <section class="card hw-section">
          <h3 class="hw-section-title">System</h3>
          ${dl([
            ["Operating System", osLabel(hw)],
            ["Kernel", shown(hw.operating_system.kernel)],
            ["Architecture", hw.architecture.name],
            ["Environment", environmentLabel(hw)],
            ["System Memory", formatCapacity(hw.memory.capacity_bytes)],
          ])}
        </section>
        <section class="card hw-section">
          <h3 class="hw-section-title">CPU</h3>
          ${dl([
            ["Model", shown(hw.cpu.model)],
            ["Vendor", shown(hw.cpu.vendor)],
            ["Sockets", shown(hw.cpu.socket_count)],
            ["Physical Cores", shown(hw.cpu.physical_core_count)],
            ["Logical Threads", shown(hw.cpu.logical_thread_count)],
          ])}
        </section>
      </div>
      <section class="card hw-section hw-accelerators">
        <div class="hw-accelerators-head">
          <h3 class="hw-section-title">Accelerators</h3>
          <span class="muted hw-accelerators-count">${hw.accelerators.length} detected</span>
        </div>
        ${
          hw.accelerators.length === 0
            ? `<p class="muted">No accelerators were detected or exposed to this process.</p>`
            : `<div class="hw-accelerators-grid">${accelerators}</div>`
        }
      </section>
      <section class="card hw-section hw-summary">
        <div class="hw-summary-head">
          <p class="muted">Plain text formatted for sharing in bug reports and support requests.</p>
          <button class="btn btn--sm" data-copy>Copy</button>
        </div>
        <textarea class="hw-summary-text" readonly aria-label="Hardware text summary" data-summary-text></textarea>
      </section>
    `;

    const summaryText = bodyEl.querySelector("[data-summary-text]");
    summaryText.value = hardwareSummary(hw);
    const copyBtn = bodyEl.querySelector("[data-copy]");
    copyBtn.addEventListener("click", async () => {
      let copied = false;
      try {
        await navigator.clipboard.writeText(summaryText.value);
        copied = true;
      } catch {
        summaryText.focus();
        summaryText.select();
        copied = document.execCommand("copy");
      }
      copyBtn.textContent = copied ? "Copied!" : "Select All";
      setTimeout(() => (copyBtn.textContent = "Copy"), 2000);
    });
  }

  (async () => {
    try {
      const hw = await getHardware();
      renderOverview(hw);
    } catch (cause) {
      bodyEl.innerHTML = `
        <div class="card hw-error">
          <h3 class="hw-error-title">Hardware detection unavailable</h3>
          <p class="muted">${escapeHtml(cause instanceof Error ? cause.message : "No hardware snapshot was captured at startup.")}</p>
        </div>`;
    }
  })();

  return {
    el: root,
    destroy() {},
  };
}
