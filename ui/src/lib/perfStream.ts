// Decoder and in-memory history for the /api/events/performance stream.
//
// The server sends named events with column-oriented JSON: each message holds
// one array per field, indexed like its "t" (unix seconds) array. On connect
// it sends meta, state and the last hour; after that one reading per message.

export interface PerfGpuInfo {
  key: string;
  name: string;
}

export interface PerfMeta {
  // seconds between readings; a missing section is disabled
  every: { system?: number; gpu?: number };
  gpus: PerfGpuInfo[];
}

export type PerfSectionState = "ok" | "paused" | "disabled";

export interface PerfState {
  system: PerfSectionState;
  gpu: PerfSectionState;
  // GPU indexes (meta order) that were runtime suspended at the last reading
  suspended?: number[];
}

export interface PerfSysMessage {
  t: number[];
  cpu: number[][]; // one series per core
  memUsed: number[];
  memTotal: number[];
  swapUsed: number[];
  swapTotal: number[];
  load1: number[];
  load5: number[];
  load15: number[];
  net: Record<string, { recv: number[]; sent: number[] }>;
}

export const GPU_FIELDS = [
  "util",
  "vramUsed",
  "vramTotal",
  "sharedUsed",
  "sharedTotal",
  "tempC",
  "vramTempC",
  "fanPct",
  "powerW",
] as const;
export type GpuField = (typeof GPU_FIELDS)[number];

// A field missing from a GPU's object was not reported by any reading in the
// message. null inside a series means not reported for that reading.
export type GpuSeries = Partial<Record<GpuField, (number | null)[]>>;

export interface PerfGpuMessage {
  t: number[];
  gpus: GpuSeries[]; // meta order
}

const HISTORY_SECONDS = 60 * 60;

/** Holds the last hour of readings merged from stream messages. */
export class PerfHistory {
  meta: PerfMeta = { every: {}, gpus: [] };
  state: PerfState = { system: "paused", gpu: "paused" };
  sys: PerfSysMessage = emptySys();
  gpu: { t: number[]; gpus: Required<GpuSeries>[] } = { t: [], gpus: [] };

  reset(): void {
    this.sys = emptySys();
    this.gpu = { t: [], gpus: [] };
  }

  setMeta(meta: PerfMeta): void {
    this.meta = meta;
    while (this.gpu.gpus.length < meta.gpus.length) {
      this.gpu.gpus.push(emptyGpuSeries(this.gpu.t.length));
    }
  }

  setState(state: PerfState): void {
    this.state = state;
  }

  addSys(msg: PerfSysMessage): void {
    const s = this.sys;
    const before = s.t.length;
    s.t.push(...msg.t);
    const cores = Math.max(s.cpu.length, msg.cpu.length);
    for (let c = 0; c < cores; c++) {
      s.cpu[c] ??= new Array(before).fill(0);
      s.cpu[c].push(...(msg.cpu[c] ?? new Array(msg.t.length).fill(0)));
    }
    s.memUsed.push(...msg.memUsed);
    s.memTotal.push(...msg.memTotal);
    s.swapUsed.push(...msg.swapUsed);
    s.swapTotal.push(...msg.swapTotal);
    s.load1.push(...msg.load1);
    s.load5.push(...msg.load5);
    s.load15.push(...msg.load15);
    const ifaces = new Set([...Object.keys(s.net), ...Object.keys(msg.net ?? {})]);
    for (const name of ifaces) {
      s.net[name] ??= { recv: new Array(before).fill(0), sent: new Array(before).fill(0) };
      const add = msg.net?.[name];
      s.net[name].recv.push(...(add?.recv ?? new Array(msg.t.length).fill(0)));
      s.net[name].sent.push(...(add?.sent ?? new Array(msg.t.length).fill(0)));
    }
    this.trim();
  }

  addGpu(msg: PerfGpuMessage): void {
    const n = msg.t.length;
    this.gpu.t.push(...msg.t);
    while (this.gpu.gpus.length < msg.gpus.length) {
      this.gpu.gpus.push(emptyGpuSeries(this.gpu.t.length - n));
    }
    this.gpu.gpus.forEach((series, i) => {
      const add = msg.gpus[i] ?? {};
      for (const f of GPU_FIELDS) {
        series[f].push(...(add[f] ?? new Array(n).fill(null)));
      }
    });
    this.trim();
  }

  /** Drops readings older than an hour before the newest one. */
  private trim(): void {
    const newest = Math.max(this.sys.t.at(-1) ?? 0, this.gpu.t.at(-1) ?? 0);
    const cutoff = newest - HISTORY_SECONDS;
    const sysFrom = firstAtOrAfter(this.sys.t, cutoff);
    if (sysFrom > 0) {
      const s = this.sys;
      s.t = s.t.slice(sysFrom);
      s.cpu = s.cpu.map((c) => c.slice(sysFrom));
      for (const k of ["memUsed", "memTotal", "swapUsed", "swapTotal", "load1", "load5", "load15"] as const) {
        s[k] = s[k].slice(sysFrom);
      }
      for (const v of Object.values(s.net)) {
        v.recv = v.recv.slice(sysFrom);
        v.sent = v.sent.slice(sysFrom);
      }
    }
    const gpuFrom = firstAtOrAfter(this.gpu.t, cutoff);
    if (gpuFrom > 0) {
      this.gpu.t = this.gpu.t.slice(gpuFrom);
      for (const series of this.gpu.gpus) {
        for (const f of GPU_FIELDS) series[f] = series[f].slice(gpuFrom);
      }
    }
  }
}

function emptySys(): PerfSysMessage {
  return {
    t: [],
    cpu: [],
    memUsed: [],
    memTotal: [],
    swapUsed: [],
    swapTotal: [],
    load1: [],
    load5: [],
    load15: [],
    net: {},
  };
}

function emptyGpuSeries(length: number): Required<GpuSeries> {
  const s = {} as Required<GpuSeries>;
  for (const f of GPU_FIELDS) s[f] = new Array(length).fill(null);
  return s;
}

function firstAtOrAfter(t: number[], cutoff: number): number {
  let i = 0;
  while (i < t.length && t[i] < cutoff) i++;
  return i;
}

/**
 * Picks the readings inside the window and marks where a line must break.
 * A gap longer than two intervals (polling was paused) gets a null point so
 * charts do not join readings across it.
 *
 * Returns the labels and, for each reading index, where it lands in the
 * output (-1 when it is outside the window).
 */
export function chartAxis(
  t: number[],
  everySeconds: number,
  windowSeconds: number,
): { labels: string[]; slots: number[]; length: number } {
  const labels: string[] = [];
  const slots = new Array(t.length).fill(-1);
  if (t.length === 0) return { labels, slots, length: 0 };
  const newest = t[t.length - 1];
  const cutoff = newest - windowSeconds;
  const maxGap = 2 * Math.max(everySeconds, 1);
  let prev: number | undefined;
  for (let i = 0; i < t.length; i++) {
    if (t[i] < cutoff) continue;
    if (prev !== undefined && t[i] - prev > maxGap) {
      labels.push("");
    }
    slots[i] = labels.length;
    labels.push(formatDelta(t[i] - newest));
    prev = t[i];
  }
  return { labels, slots, length: labels.length };
}

/** Places a series on a chart axis from chartAxis, with null in gaps. */
export function onAxis<T extends number | null>(
  values: T[],
  axis: { slots: number[]; length: number },
): (number | null)[] {
  const out: (number | null)[] = new Array(axis.length).fill(null);
  axis.slots.forEach((slot, i) => {
    if (slot >= 0) out[slot] = values[i] ?? null;
  });
  return out;
}

/** Formats seconds relative to the newest reading, like -45s or -2:30. */
export function formatDelta(diffSec: number): string {
  const abs = Math.abs(Math.round(diffSec));
  const sign = diffSec <= 0 ? "-" : "+";
  if (abs < 60) return `${sign}${abs}s`;
  const min = Math.floor(abs / 60);
  const sec = abs % 60;
  if (sec === 0) return `${sign}${min}m`;
  return `${sign}${min}:${sec.toString().padStart(2, "0")}`;
}

/** Whether any reading of any GPU reported field. */
export function hasGpuField(history: PerfHistory, field: GpuField): boolean {
  return history.gpu.gpus.some((s) => s[field].some((v) => v !== null));
}

/** Last non-null value of a series. */
export function lastValue(values: (number | null)[]): number | null {
  for (let i = values.length - 1; i >= 0; i--) {
    if (values[i] !== null) return values[i];
  }
  return null;
}
