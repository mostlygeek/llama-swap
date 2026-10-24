import { describe, it, expect } from "vitest";
import { PerfHistory, chartAxis, onAxis, formatDelta, hasGpuField, lastValue, type PerfSysMessage } from "./perfStream";

function sysMsg(t: number[], mem: number): PerfSysMessage {
  return {
    t,
    cpu: [t.map(() => 10)],
    memUsed: t.map(() => mem),
    memTotal: t.map(() => 1000),
    swapUsed: t.map(() => 0),
    swapTotal: t.map(() => 0),
    load1: t.map(() => 1),
    load5: t.map(() => 1),
    load15: t.map(() => 1),
    net: { eth0: { recv: t.map(() => 5), sent: t.map(() => 6) } },
  };
}

describe("PerfHistory", () => {
  it("appends system history and live readings", () => {
    const h = new PerfHistory();
    h.addSys(sysMsg([100, 105], 200));
    h.addSys(sysMsg([110], 300));
    expect(h.sys.t).toEqual([100, 105, 110]);
    expect(h.sys.memUsed).toEqual([200, 200, 300]);
    expect(h.sys.net.eth0.recv).toEqual([5, 5, 5]);
  });

  it("pads interfaces and cores that appear later", () => {
    const h = new PerfHistory();
    h.addSys({ ...sysMsg([100], 1), net: {} });
    h.addSys(sysMsg([105], 1));
    expect(h.sys.net.eth0.recv).toEqual([0, 5]);
  });

  it("aligns GPU series and fills nulls for unreported fields", () => {
    const h = new PerfHistory();
    h.setMeta({ every: { gpu: 5 }, gpus: [{ key: "a", name: "A" }] });
    h.addGpu({ t: [100, 105], gpus: [{ util: [50, 60] }] });
    // a second GPU shows up in a live reading
    h.setMeta({ every: { gpu: 5 }, gpus: [{ key: "a", name: "A" }, { key: "b", name: "B" }] });
    h.addGpu({ t: [110], gpus: [{ util: [70], powerW: [300] }, { util: [5] }] });

    expect(h.gpu.t).toEqual([100, 105, 110]);
    expect(h.gpu.gpus[0].util).toEqual([50, 60, 70]);
    expect(h.gpu.gpus[0].powerW).toEqual([null, null, 300]);
    expect(h.gpu.gpus[1].util).toEqual([null, null, 5]);
    expect(hasGpuField(h, "powerW")).toBe(true);
    expect(hasGpuField(h, "fanPct")).toBe(false);
  });

  it("drops GPU series when the GPU order changes", () => {
    const h = new PerfHistory();
    h.setMeta({ every: { gpu: 5 }, gpus: [{ key: "a", name: "A" }] });
    h.addGpu({ t: [100], gpus: [{ util: [50] }] });
    h.setMeta({ every: { gpu: 5 }, gpus: [{ key: "b", name: "B" }] });
    expect(h.gpu.t).toEqual([]);
    expect(h.gpu.gpus).toHaveLength(1);
    expect(h.gpu.gpus[0].util).toEqual([]);
  });

  it("keeps only the last hour", () => {
    const h = new PerfHistory();
    h.addSys(sysMsg([0, 1000], 1));
    h.addSys(sysMsg([4000], 2));
    expect(h.sys.t).toEqual([1000, 4000]);
    expect(h.sys.cpu[0]).toHaveLength(2);
  });

  it("reset clears readings but keeps meta", () => {
    const h = new PerfHistory();
    h.setMeta({ every: { system: 5 }, gpus: [] });
    h.addSys(sysMsg([1], 1));
    h.reset();
    expect(h.sys.t).toEqual([]);
    expect(h.meta.every.system).toBe(5);
  });
});

describe("chartAxis", () => {
  it("breaks the line across a paused gap", () => {
    const axis = chartAxis([100, 105, 110, 200, 205], 5, 3600);
    expect(axis.labels).toEqual(["-1:45", "-1:40", "-1:35", "", "-5s", "-0s"]);
    expect(onAxis([1, 2, 3, 4, 5], axis)).toEqual([1, 2, 3, null, 4, 5]);
  });

  it("drops readings outside the window", () => {
    const axis = chartAxis([0, 100, 200], 60, 150);
    expect(axis.labels).toEqual(["-1:40", "-0s"]);
    expect(onAxis([1, 2, 3], axis)).toEqual([2, 3]);
  });

  it("handles empty input", () => {
    expect(chartAxis([], 5, 60)).toEqual({ labels: [], slots: [], length: 0 });
  });
});

describe("helpers", () => {
  it("formats deltas", () => {
    expect(formatDelta(-45)).toBe("-45s");
    expect(formatDelta(-120)).toBe("-2m");
    expect(formatDelta(-150)).toBe("-2:30");
  });

  it("finds the last reported value", () => {
    expect(lastValue([1, null, 3, null])).toBe(3);
    expect(lastValue([null])).toBeNull();
  });
});
