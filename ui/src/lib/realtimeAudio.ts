/**
 * Audio and URL helpers for the Realtime playground.
 *
 * The realtime API (OpenAI style, served by llama-server at /v1/realtime)
 * exchanges little-endian PCM16 mono audio as base64 strings inside JSON
 * events. Input is 16 kHz and output 24 kHz by default; the session.created
 * event states the actual rates, so callers pass them in rather than assume.
 */

/** Build the ws:// or wss:// URL for /v1/realtime on the page's own origin. */
export function realtimeUrl(path: string, model: string, location: { protocol: string; host: string }): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}${path}?model=${encodeURIComponent(model)}`;
}

/**
 * Linearly resample mono float samples from one rate to another. Microphones
 * run at the AudioContext's rate (usually 48 kHz), which is never what the
 * server wants, and asking for a 16 kHz context breaks MediaStream sources in
 * Firefox, so the conversion happens here.
 */
export function resample(input: Float32Array, fromRate: number, toRate: number): Float32Array {
  if (fromRate === toRate || input.length === 0) return input;
  const ratio = fromRate / toRate;
  const length = Math.floor(input.length / ratio);
  const out = new Float32Array(length);
  for (let i = 0; i < length; i++) {
    const pos = i * ratio;
    const idx = Math.floor(pos);
    const frac = pos - idx;
    const a = input[idx];
    const b = idx + 1 < input.length ? input[idx + 1] : a;
    out[i] = a + (b - a) * frac;
  }
  return out;
}

/** Convert [-1, 1] float samples to PCM16, clamping anything out of range. */
export function floatToPcm16(input: Float32Array): Int16Array {
  const out = new Int16Array(input.length);
  for (let i = 0; i < input.length; i++) {
    const s = Math.max(-1, Math.min(1, input[i]));
    out[i] = s < 0 ? s * 0x8000 : s * 0x7fff;
  }
  return out;
}

/** Convert PCM16 samples to [-1, 1] floats for an AudioBuffer. */
export function pcm16ToFloat(input: Int16Array): Float32Array {
  const out = new Float32Array(input.length);
  for (let i = 0; i < input.length; i++) {
    out[i] = input[i] / (input[i] < 0 ? 0x8000 : 0x7fff);
  }
  return out;
}

export function pcm16ToBase64(pcm: Int16Array): string {
  const bytes = new Uint8Array(pcm.buffer, pcm.byteOffset, pcm.byteLength);
  let binary = "";
  // Chunked so String.fromCharCode never gets more arguments than the engine
  // allows.
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary);
}

export function base64ToPcm16(b64: string): Int16Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length & ~1);
  for (let i = 0; i < bytes.length; i++) bytes[i] = binary.charCodeAt(i);
  return new Int16Array(bytes.buffer);
}

/** Root mean square level of a block of samples, 0..1. */
export function rms(input: Float32Array): number {
  if (input.length === 0) return 0;
  let sum = 0;
  for (let i = 0; i < input.length; i++) sum += input[i] * input[i];
  return Math.sqrt(sum / input.length);
}

/**
 * A minimal energy based voice activity detector, for hands-free mode.
 * llama-server's realtime sessions have no server side turn detection, so the
 * browser decides when a turn ends and commits the buffer itself.
 *
 * feed() takes the level of each audio block and its duration, and returns
 * "start" when speech begins, "end" once it has been quiet for silenceMs
 * after at least minSpeechMs of speech, "discard" when the sound was too
 * short to be a turn (a cough, a click), and null otherwise.
 */
export class EnergyVAD {
  private speaking = false;
  private speechMs = 0;
  private silenceMs = 0;

  constructor(
    public threshold = 0.02,
    public silenceLimitMs = 700,
    public minSpeechMs = 250,
  ) {}

  get isSpeaking(): boolean {
    return this.speaking;
  }

  /** How long it has been quiet since speech, 0 while loud or idle. */
  get quietMs(): number {
    return this.silenceMs;
  }

  reset(): void {
    this.speaking = false;
    this.speechMs = 0;
    this.silenceMs = 0;
  }

  feed(level: number, blockMs: number): "start" | "end" | "discard" | null {
    const loud = level >= this.threshold;
    if (!this.speaking) {
      if (loud) {
        this.speaking = true;
        this.speechMs = blockMs;
        this.silenceMs = 0;
        return "start";
      }
      return null;
    }
    if (loud) {
      this.speechMs += blockMs;
      this.silenceMs = 0;
      return null;
    }
    this.silenceMs += blockMs;
    if (this.silenceMs >= this.silenceLimitMs) {
      const longEnough = this.speechMs >= this.minSpeechMs;
      this.reset();
      return longEnough ? "end" : "discard";
    }
    return null;
  }
}

/** Join PCM16 chunks into one buffer. */
export function concatPcm16(chunks: Int16Array[]): Int16Array {
  const out = new Int16Array(chunks.reduce((n, c) => n + c.length, 0));
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.length;
  }
  return out;
}

/** Wrap mono PCM16 samples in a WAV container so an <audio> element can play them. */
export function encodeWav(pcm: Int16Array, sampleRate: number): Blob {
  const header = new DataView(new ArrayBuffer(44));
  const writeStr = (offset: number, s: string) => {
    for (let i = 0; i < s.length; i++) header.setUint8(offset + i, s.charCodeAt(i));
  };
  writeStr(0, "RIFF");
  header.setUint32(4, 36 + pcm.byteLength, true);
  writeStr(8, "WAVE");
  writeStr(12, "fmt ");
  header.setUint32(16, 16, true); // fmt chunk size
  header.setUint16(20, 1, true); // PCM
  header.setUint16(22, 1, true); // mono
  header.setUint32(24, sampleRate, true);
  header.setUint32(28, sampleRate * 2, true); // byte rate
  header.setUint16(32, 2, true); // block align
  header.setUint16(34, 16, true); // bits per sample
  writeStr(36, "data");
  header.setUint32(40, pcm.byteLength, true);
  return new Blob([header.buffer, pcm.slice().buffer], { type: "audio/wav" });
}
