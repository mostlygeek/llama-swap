import { describe, expect, it } from "vitest";
import { buildAdvancedGpuIO, hasAdvancedGpuIO } from "./performanceGpu";
import type { GpuStat } from "./types";

function gpu(overrides: Partial<GpuStat>): GpuStat {
  return {
    timestamp: "2026-07-11T12:00:00Z",
    id: 0,
    name: "Intel Arc Pro B70",
    uuid: "gpu-0",
    temp_c: 0,
    vram_temp_c: 0,
    gpu_util_pct: 0,
    mem_util_pct: 0,
    mem_used_mb: 0,
    mem_total_mb: 0,
    fan_speed_pct: 0,
    power_draw_w: 0,
    ...overrides,
  };
}

describe("advanced GPU I/O", () => {
  it("stays hidden without optional telemetry", () => {
    expect(hasAdvancedGpuIO([gpu({})])).toBe(false);
  });

  it("detects available optional telemetry including zero values", () => {
    expect(hasAdvancedGpuIO([gpu({ graphics_clock_mhz: 0 })])).toBe(true);
  });

  it("keeps multi-GPU bandwidth series separate and gaps missing samples", () => {
    const timestamps = ["2026-07-11T12:00:00Z", "2026-07-11T12:00:05Z"];
    const advanced = buildAdvancedGpuIO(
      [
        gpu({ timestamp: timestamps[0], mem_read_bandwidth_kbps: 1000 }),
        gpu({ timestamp: timestamps[1], mem_write_bandwidth_kbps: 2000 }),
        gpu({ id: 3, uuid: "gpu-3", name: "Intel Arc A770", timestamp: timestamps[0], mem_read_bandwidth_kbps: 3000 }),
      ],
      timestamps,
    );

    expect(advanced.memoryBandwidthDatasets).toEqual([
      { label: "Intel Arc Pro B70 Memory Read", data: [1_000_000, null] },
      { label: "Intel Arc Pro B70 Memory Write", data: [null, 2_000_000] },
      { label: "Intel Arc A770 Memory Read", data: [3_000_000, null] },
    ]);
  });

  it("keeps PCIe directions separate and selects the latest clock references", () => {
    const timestamps = ["2026-07-11T12:00:00Z", "2026-07-11T12:00:05Z"];
    const advanced = buildAdvancedGpuIO(
      [
        gpu({ timestamp: timestamps[0], pcie_rx_mbps: 12.5, graphics_clock_mhz: 1200, graphics_clock_max_mhz: 2800 }),
        gpu({ timestamp: timestamps[1], pcie_tx_mbps: 24, graphics_clock_mhz: 1600, graphics_clock_max_mhz: 3000 }),
      ],
      timestamps,
    );

    expect(advanced.pcieDatasets).toEqual([
      { label: "Intel Arc Pro B70 PCIe RX", data: [12_500_000, null] },
      { label: "Intel Arc Pro B70 PCIe TX", data: [null, 24_000_000] },
    ]);
    expect(advanced.graphicsClockDatasets).toEqual([
      { label: "Intel Arc Pro B70 Graphics Clock", data: [1200, 1600] },
    ]);
    expect(advanced.graphicsClockMaximums).toEqual([{ id: 0, name: "Intel Arc Pro B70", value: 3000 }]);
  });
});
