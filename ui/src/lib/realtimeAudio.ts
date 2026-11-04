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
 * Resample a stream of mono float samples from one rate to another, fed in
 * blocks. Microphones run at the AudioContext's rate (usually 48 kHz), which
 * is never what the server wants, and asking for a 16 kHz context breaks
 * MediaStream sources in Firefox, so the conversion happens here.
 *
 * Going down, a windowed-sinc low-pass first removes what the new rate cannot
 * hold; without it a 12 kHz hiss folds back as a false 4 kHz tone. The filter
 * history and the read position carry across blocks, so splitting the stream
 * into blocks gives the same output as one long block.
 */
export class Resampler {
  private readonly ratio: number;
  private readonly taps: Float32Array;
  // The last taps.length - 1 input samples, the filter's history.
  private history: Float32Array;
  // Next output position in the current block's filtered samples; -1 is the
  // previous block's last sample, kept in prev.
  private pos = 0;
  private prev = 0;

  constructor(
    readonly fromRate: number,
    readonly toRate: number,
    numTaps = 63,
  ) {
    this.ratio = fromRate / toRate;
    this.taps = toRate < fromRate ? lowPass(numTaps, (0.45 * toRate) / fromRate) : new Float32Array([1]);
    this.history = new Float32Array(this.taps.length - 1);
  }

  process(input: Float32Array): Float32Array {
    if (this.fromRate === this.toRate || input.length === 0) return input;
    const taps = this.taps;
    const hist = this.history.length;
    const x = new Float32Array(hist + input.length);
    x.set(this.history);
    x.set(input, hist);
    const y = new Float32Array(input.length);
    for (let i = 0; i < y.length; i++) {
      let acc = 0;
      for (let k = 0; k < taps.length; k++) acc += taps[k] * x[hist + i - k];
      y[i] = acc;
    }
    this.history = x.slice(x.length - hist);

    const out: number[] = [];
    while (this.pos < y.length - 1) {
      const idx = Math.floor(this.pos);
      const frac = this.pos - idx;
      const a = idx < 0 ? this.prev : y[idx];
      out.push(a + (y[idx + 1] - a) * frac);
      this.pos += this.ratio;
    }
    this.pos -= y.length;
    this.prev = y[y.length - 1];
    return Float32Array.from(out);
  }
}

/** Blackman-windowed sinc low-pass, cutoff in cycles per sample, unity DC gain. */
function lowPass(numTaps: number, cutoff: number): Float32Array {
  const taps = new Float32Array(numTaps);
  const mid = (numTaps - 1) / 2;
  let sum = 0;
  for (let i = 0; i < numTaps; i++) {
    const t = i - mid;
    const sinc = t === 0 ? 2 * cutoff : Math.sin(2 * Math.PI * cutoff * t) / (Math.PI * t);
    const w = 0.42 - 0.5 * Math.cos((2 * Math.PI * i) / (numTaps - 1)) + 0.08 * Math.cos((4 * Math.PI * i) / (numTaps - 1));
    taps[i] = sinc * w;
    sum += taps[i];
  }
  for (let i = 0; i < numTaps; i++) taps[i] /= sum;
  return taps;
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
