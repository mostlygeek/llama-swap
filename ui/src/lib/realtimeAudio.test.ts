import { describe, expect, it } from "vitest";
import {
  EnergyVAD,
  concatPcm16,
  encodeWav,
  base64ToPcm16,
  floatToPcm16,
  pcm16ToBase64,
  pcm16ToFloat,
  realtimeUrl,
  resample,
  rms,
} from "./realtimeAudio";

describe("realtimeUrl", () => {
  it("uses ws for http and escapes the model", () => {
    expect(realtimeUrl("/v1/realtime", "a/b c", { protocol: "http:", host: "localhost:8080" })).toBe(
      "ws://localhost:8080/v1/realtime?model=a%2Fb%20c",
    );
  });

  it("uses wss for https and keeps a mount prefix", () => {
    expect(realtimeUrl("/p/v1/realtime", "m", { protocol: "https:", host: "h" })).toBe("wss://h/p/v1/realtime?model=m");
  });
});

describe("resample", () => {
  it("returns the input when the rates match", () => {
    const input = new Float32Array([0.1, 0.2]);
    expect(resample(input, 16000, 16000)).toBe(input);
  });

  it("downsamples 48 kHz to 16 kHz by a third", () => {
    const input = new Float32Array(4800).fill(0.5);
    const out = resample(input, 48000, 16000);
    expect(out.length).toBe(1600);
    expect(out.every((v) => Math.abs(v - 0.5) < 1e-6)).toBe(true);
  });

  it("interpolates between samples", () => {
    const out = resample(new Float32Array([0, 1, 0, 1]), 2, 4);
    expect(Array.from(out.slice(0, 3))).toEqual([0, 0.5, 1]);
  });
});

describe("PCM16 conversion", () => {
  it("clamps out of range floats", () => {
    expect(Array.from(floatToPcm16(new Float32Array([-2, -1, 0, 1, 2])))).toEqual([-32768, -32768, 0, 32767, 32767]);
  });

  it("round trips through base64", () => {
    const pcm = new Int16Array([0, 1, -1, 32767, -32768, 1234]);
    expect(Array.from(base64ToPcm16(pcm16ToBase64(pcm)))).toEqual(Array.from(pcm));
  });

  it("round trips a large buffer", () => {
    const pcm = new Int16Array(100_000).map((_, i) => (i * 7) % 65536 - 32768);
    expect(base64ToPcm16(pcm16ToBase64(pcm))).toEqual(pcm);
  });

  it("converts PCM16 back to floats in range", () => {
    expect(Array.from(pcm16ToFloat(new Int16Array([-32768, 0, 32767])))).toEqual([-1, 0, 1]);
  });
});

describe("rms", () => {
  it("is zero for silence and empty input", () => {
    expect(rms(new Float32Array(10))).toBe(0);
    expect(rms(new Float32Array())).toBe(0);
  });

  it("is the amplitude of a constant signal", () => {
    expect(rms(new Float32Array(10).fill(0.5))).toBeCloseTo(0.5);
  });
});

describe("EnergyVAD", () => {
  const loud = 0.5;
  const quiet = 0;

  it("reports start then end after enough silence", () => {
    const vad = new EnergyVAD(0.02, 300, 100);
    expect(vad.feed(loud, 50)).toBe("start");
    expect(vad.feed(loud, 50)).toBe(null);
    expect(vad.isSpeaking).toBe(true);
    expect(vad.feed(quiet, 100)).toBe(null);
    expect(vad.feed(quiet, 100)).toBe(null);
    expect(vad.feed(quiet, 100)).toBe("end");
    expect(vad.isSpeaking).toBe(false);
  });

  it("discards sounds too short to be speech", () => {
    const vad = new EnergyVAD(0.02, 100, 250);
    expect(vad.feed(loud, 50)).toBe("start");
    expect(vad.feed(quiet, 100)).toBe("discard");
  });

  it("restarts the silence count when speech resumes", () => {
    const vad = new EnergyVAD(0.02, 200, 0);
    vad.feed(loud, 50);
    vad.feed(quiet, 150);
    vad.feed(loud, 50);
    expect(vad.feed(quiet, 150)).toBe(null);
    expect(vad.feed(quiet, 50)).toBe("end");
  });

  it("reports how long it has been quiet mid-turn", () => {
    const vad = new EnergyVAD(0.02, 500, 0);
    vad.feed(loud, 50);
    expect(vad.quietMs).toBe(0);
    vad.feed(quiet, 40);
    vad.feed(quiet, 40);
    expect(vad.quietMs).toBe(80);
    vad.feed(loud, 40);
    expect(vad.quietMs).toBe(0);
  });

  it("stays idle on silence", () => {
    const vad = new EnergyVAD();
    expect(vad.feed(quiet, 1000)).toBe(null);
  });
});

describe("encodeWav", () => {
  it("writes a 44 byte header followed by the samples", async () => {
    const pcm = concatPcm16([new Int16Array([1, 2]), new Int16Array([3])]);
    const blob = encodeWav(pcm, 24000);
    expect(blob.type).toBe("audio/wav");
    const view = new DataView(await blob.arrayBuffer());
    expect(view.byteLength).toBe(44 + 6);
    expect(String.fromCharCode(view.getUint8(0), view.getUint8(1), view.getUint8(2), view.getUint8(3))).toBe("RIFF");
    expect(view.getUint32(24, true)).toBe(24000);
    expect(view.getUint32(40, true)).toBe(6);
    expect(view.getInt16(44, true)).toBe(1);
    expect(view.getInt16(48, true)).toBe(3);
  });
});
