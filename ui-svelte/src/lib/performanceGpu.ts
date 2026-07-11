import type { GpuStat } from "./types";

export type GpuDataset = {
  label: string;
  data: Array<number | null>;
};

type OptionalGpuField = keyof Pick<
  GpuStat,
  | "mem_read_bandwidth_kbps"
  | "mem_write_bandwidth_kbps"
  | "mem_bandwidth_util_pct"
  | "pcie_rx_mbps"
  | "pcie_tx_mbps"
  | "graphics_clock_mhz"
  | "graphics_clock_max_mhz"
>;

type FieldDefinition = {
  key: OptionalGpuField;
  label: string;
  multiplier?: number;
};

export type GpuReference = {
  id: number;
  name: string;
  value: number;
};

const advancedFields: OptionalGpuField[] = [
  "mem_read_bandwidth_kbps",
  "mem_write_bandwidth_kbps",
  "mem_bandwidth_util_pct",
  "pcie_rx_mbps",
  "pcie_tx_mbps",
  "graphics_clock_mhz",
  "graphics_clock_max_mhz",
];

function gpuIdentity(gpu: GpuStat): string {
  return gpu.uuid || String(gpu.id);
}

export function hasAdvancedGpuIO(stats: GpuStat[]): boolean {
  return stats.some((stat) => advancedFields.some((field) => typeof stat[field] === "number"));
}

export function buildOptionalGpuDatasets(stats: GpuStat[], timestamps: string[], fields: FieldDefinition[]): GpuDataset[] {
  const indexByTimestamp = new Map(timestamps.map((timestamp, index) => [timestamp, index]));
  const byGpu = new Map<string, { id: number; name: string; values: Map<OptionalGpuField, Array<number | null>> }>();

  for (const stat of stats) {
    const identity = gpuIdentity(stat);
    if (!byGpu.has(identity)) {
      byGpu.set(identity, {
        id: stat.id,
        name: stat.name,
        values: new Map(fields.map((field) => [field.key, Array<number | null>(timestamps.length).fill(null)])),
      });
    }

    const index = indexByTimestamp.get(stat.timestamp);
    if (index === undefined) continue;
    const entry = byGpu.get(identity)!;
    for (const field of fields) {
      const value = stat[field.key];
      if (typeof value === "number") entry.values.get(field.key)![index] = value * (field.multiplier ?? 1);
    }
  }

  const datasets: GpuDataset[] = [];
  for (const entry of byGpu.values()) {
    for (const field of fields) {
      const values = entry.values.get(field.key)!;
      if (values.every((value) => value === null)) continue;
      datasets.push({
        label: `${entry.name || `GPU ${entry.id}`} ${field.label}`,
        data: values,
      });
    }
  }
  return datasets;
}

export function latestOptionalGpuValues(stats: GpuStat[], field: OptionalGpuField): GpuReference[] {
  const latest = new Map<string, GpuReference>();
  for (const stat of stats) {
    const value = stat[field];
    if (typeof value === "number") {
      latest.set(gpuIdentity(stat), { id: stat.id, name: stat.name, value });
    }
  }
  return [...latest.values()];
}

export function buildAdvancedGpuIO(stats: GpuStat[], timestamps: string[]) {
  return {
    memoryBandwidthDatasets: buildOptionalGpuDatasets(stats, timestamps, [
      { key: "mem_read_bandwidth_kbps", label: "Memory Read", multiplier: 1000 },
      { key: "mem_write_bandwidth_kbps", label: "Memory Write", multiplier: 1000 },
    ]),
    pcieDatasets: buildOptionalGpuDatasets(stats, timestamps, [
      { key: "pcie_rx_mbps", label: "PCIe RX", multiplier: 1000 * 1000 },
      { key: "pcie_tx_mbps", label: "PCIe TX", multiplier: 1000 * 1000 },
    ]),
    graphicsClockDatasets: buildOptionalGpuDatasets(stats, timestamps, [{ key: "graphics_clock_mhz", label: "Graphics Clock" }]),
    memoryBandwidthUtilization: latestOptionalGpuValues(stats, "mem_bandwidth_util_pct"),
    graphicsClockMaximums: latestOptionalGpuValues(stats, "graphics_clock_max_mhz"),
  };
}
