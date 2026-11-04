<script lang="ts">
  import { tick } from "svelte";
  import { get } from "svelte/store";
  import { hasListedModels } from "../../stores/api";
  import { apiUrl } from "../../lib/apiPath";
  import { persistentStore } from "../../stores/persistent";
  import { playgroundStores } from "../../stores/playgroundActivity";
  import {
    EnergyVAD,
    base64ToPcm16,
    concatPcm16,
    encodeWav,
    floatToPcm16,
    pcm16ToBase64,
    pcm16ToFloat,
    realtimeUrl,
    Resampler,
    rms,
  } from "../../lib/realtimeAudio";
  import ModelSelector from "./ModelSelector.svelte";
  import EmptyState from "../EmptyState.svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import * as Dialog from "$lib/components/ui/dialog/index.js";
  import { Label } from "$lib/components/ui/label/index.js";
  import { Textarea } from "$lib/components/ui/textarea/index.js";
  import { Download, LoaderCircle, Mic, MicOff, Phone, PhoneOff, Play, Send, Settings, Square, Trash2 } from "@lucide/svelte";
  import { isSubmitEnter } from "../../lib/ime";

  type Status = "idle" | "connecting" | "connected" | "error";
  type MicMode = "ptt" | "vad";

  interface Turn {
    id: string;
    role: "user" | "assistant" | "error";
    text: string;
    /** User audio, or the assistant's spoken reply, as a playable WAV. */
    audioUrl?: string;
    audioSeconds?: number;
    pending?: boolean;
    cancelled?: boolean;
  }

  interface LoggedEvent {
    dir: "in" | "out";
    type: string;
    time: string;
    body: string;
  }

  // Replies are spoken, so the default asks for short, plain conversation:
  // lists and markdown read aloud badly. The first line is LFM Audio's own
  // prompt for its interleaved text and speech mode; without it the model can
  // treat the user's audio as something to describe or transcribe rather
  // than answer.
  const DEFAULT_SYSTEM_PROMPT =
    "Respond with interleaved text and audio.\n\n" +
    "You are a warm, friendly voice assistant having a casual spoken conversation. " +
    "Your replies are read aloud, so keep them short and natural: one to three sentences, " +
    "plain conversational language, no lists, markdown, emoji, or code. Be curious and upbeat, " +
    "ask a follow-up question when it fits, and if you did not catch or understand something, " +
    "say so and ask the user to repeat it.";

  const selectedModelStore = persistentStore<string>("playground-realtime-model", "");
  const micModeStore = persistentStore<MicMode>("playground-realtime-mic-mode", "ptt");
  const systemPromptStore = persistentStore<string>("playground-realtime-system-prompt", DEFAULT_SYSTEM_PROMPT);
  // The store saves its default on first load, so a browser that never
  // edited the prompt holds the previous default, which lacked the
  // interleaved mode line. Move it to the current one; edited prompts stay.
  if (get(systemPromptStore) === DEFAULT_SYSTEM_PROMPT.replace("Respond with interleaved text and audio.\n\n", "")) {
    systemPromptStore.set(DEFAULT_SYSTEM_PROMPT);
  }
  // Hands-free voice detection, see EnergyVAD. Applied live, no reconnect.
  const VAD_DEFAULTS = { threshold: 0.02, pauseMs: 700, minSpeechMs: 250, prerollMs: 300 };
  const vadThresholdStore = persistentStore<number>("playground-realtime-vad-threshold", VAD_DEFAULTS.threshold);
  const vadPauseStore = persistentStore<number>("playground-realtime-vad-pause-ms", VAD_DEFAULTS.pauseMs);
  const vadMinSpeechStore = persistentStore<number>("playground-realtime-vad-min-speech-ms", VAD_DEFAULTS.minSpeechMs);
  const vadPrerollStore = persistentStore<number>("playground-realtime-vad-preroll-ms", VAD_DEFAULTS.prerollMs);
  const connectedStore = playgroundStores.realtimeConnected;

  // Always ask for text and audio together. Models like LFM Audio speak by
  // interleaving text and audio tokens, the text steering what is said; asked
  // for audio alone they stop after a second or two of garbled speech.
  const OUTPUT_MODALITIES = ["text", "audio"];

  const MAX_LOGGED_EVENTS = 300;

  let status = $state<Status>("idle");
  let statusDetail = $state("");
  let turns = $state<Turn[]>([]);
  let events = $state<LoggedEvent[]>([]);
  let textInput = $state("");
  let micOn = $state(false);
  let capturing = $state(false);
  let micLevel = $state(0);
  let assistantSpeaking = $state(false);
  let responseActive = $state(false);
  // The reply being generated, and replies the user talked over: their audio
  // still in flight from the server is kept for replay but not played.
  let activeResponseId: string | null = null;
  const silencedResponses = new Set<string>();
  let transcriptEl = $state<HTMLDivElement | null>(null);
  let showSettings = $state(false);
  // the turn whose recorded audio is being replayed
  let replayingId = $state<string | null>(null);
  let replayEl: HTMLAudioElement | null = null;

  let ws: WebSocket | null = null;
  let inputRate = 16000;
  let outputRate = 24000;

  // microphone
  let micStream: MediaStream | null = null;
  let micCtx: AudioContext | null = null;
  let micNode: AudioWorkletNode | null = null;
  let micResampler: Resampler | null = null;
  // Push-to-talk is held down. The mic can take a while to open (permission
  // prompt), and a release during that wait must cancel the turn.
  let pttHeld = false;
  let micBlock: Float32Array[] = [];
  let micBlockLen = 0;
  let preroll: Int16Array[] = [];
  let prerollLen = 0;
  let sentChunks: Int16Array[] = [];
  const vad = new EnergyVAD();

  // playback
  let playCtx: AudioContext | null = null;
  let playHead = 0;
  let playSources = new Set<AudioBufferSourceNode>();
  // response id -> received audio, turned into a WAV when the response ends
  const responseAudio = new Map<string, Int16Array[]>();
  // response id -> turn id
  const responseTurns = new Map<string, string>();
  // conversation item id -> turn id, for input audio transcriptions
  const itemTurns = new Map<string, string>();
  let pendingUserTurn: string | null = null;

  let turnSeq = 0;
  const nextTurnId = () => `t${++turnSeq}`;

  let isConnected = $derived(status === "connected");

  function logEvent(dir: "in" | "out", ev: Record<string, unknown>) {
    const body = JSON.stringify(ev, (k, v) =>
      (k === "audio" || k === "delta") && typeof v === "string" && v.length > 120 ? `<${v.length} bytes base64>` : v
    );
    const entry = { dir, type: String(ev.type ?? "?"), time: new Date().toLocaleTimeString(), body };
    events = [...events.slice(-(MAX_LOGGED_EVENTS - 1)), entry];
  }

  function send(ev: Record<string, unknown>) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(JSON.stringify(ev));
    // Appends arrive ~20 times a second; logging each would bury everything else.
    if (ev.type !== "input_audio_buffer.append") logEvent("out", ev);
  }

  function updateTurn(id: string, patch: Partial<Turn>) {
    turns = turns.map((t) => (t.id === id ? { ...t, ...patch } : t));
  }

  function addTurn(turn: Turn) {
    turns = [...turns, turn];
  }

  function addError(message: string) {
    addTurn({ id: nextTurnId(), role: "error", text: message });
  }

  $effect(() => {
    void turns;
    void capturing;
    tick().then(() => transcriptEl?.scrollTo({ top: transcriptEl.scrollHeight }));
  });

  // ---- connection -------------------------------------------------------

  function connect() {
    const model = $selectedModelStore;
    if (!model || ws) return;
    status = "connecting";
    statusDetail = "connecting, the model may need to load first...";
    const url = realtimeUrl(apiUrl("/v1/realtime"), model, window.location);
    const socket = new WebSocket(url);
    ws = socket;

    socket.onmessage = (msg) => {
      if (ws !== socket) return;
      let ev: Record<string, any>;
      try {
        ev = JSON.parse(msg.data);
      } catch {
        return;
      }
      handleEvent(ev);
    };
    socket.onerror = () => {
      if (ws !== socket) return;
      statusDetail = "websocket error";
    };
    socket.onclose = (e) => {
      if (ws !== socket) return;
      const wasConnected = status === "connected";
      teardown();
      if (!wasConnected || e.code !== 1000) {
        status = "error";
        statusDetail = e.reason || `connection closed (code ${e.code})`;
      }
    };
  }

  function disconnect() {
    ws?.close(1000);
    teardown();
  }

  function teardown() {
    ws = null;
    status = "idle";
    statusDetail = "";
    responseActive = false;
    activeResponseId = null;
    silencedResponses.clear();
    stopMic();
    stopPlayback();
    connectedStore.set(false);
    for (const [, id] of responseTurns) updateTurn(id, { pending: false });
    responseTurns.clear();
    responseAudio.clear();
    itemTurns.clear();
    pendingUserTurn = null;
  }

  function handleEvent(ev: Record<string, any>) {
    const type: string = ev.type ?? "";
    if (type !== "response.output_audio.delta") logEvent("in", ev);

    switch (type) {
      case "session.created": {
        const audio = ev.session?.audio;
        inputRate = audio?.input?.format?.rate ?? 16000;
        outputRate = audio?.output?.format?.rate ?? 24000;
        const session: Record<string, unknown> = { type: "realtime", output_modalities: OUTPUT_MODALITIES };
        if ($systemPromptStore.trim()) session.instructions = $systemPromptStore.trim();
        send({ type: "session.update", session });
        status = "connected";
        statusDetail = `in ${inputRate / 1000} kHz / out ${outputRate / 1000} kHz`;
        connectedStore.set(true);
        // Hands-free means talking right away; the Connect click is the
        // user's go-ahead, so don't make them click again for the mic.
        if ($micModeStore === "vad") void startMic();
        break;
      }
      case "input_audio_buffer.committed":
        if (pendingUserTurn && ev.item_id) itemTurns.set(ev.item_id, pendingUserTurn);
        break;
      case "conversation.item.input_audio_transcription.delta": {
        const id = itemTurns.get(ev.item_id);
        if (id) {
          const turn = turns.find((t) => t.id === id);
          updateTurn(id, { text: (turn?.text ?? "") + (ev.delta ?? "") });
        }
        break;
      }
      case "conversation.item.input_audio_transcription.completed": {
        const id = itemTurns.get(ev.item_id);
        if (id && typeof ev.transcript === "string") updateTurn(id, { text: ev.transcript });
        break;
      }
      case "response.created": {
        responseActive = true;
        activeResponseId = ev.response.id;
        const id = nextTurnId();
        responseTurns.set(ev.response.id, id);
        responseAudio.set(ev.response.id, []);
        addTurn({ id, role: "assistant", text: "", pending: true });
        break;
      }
      case "response.output_text.delta":
      case "response.output_audio_transcript.delta": {
        const id = responseTurns.get(ev.response_id);
        if (id) {
          const turn = turns.find((t) => t.id === id);
          updateTurn(id, { text: (turn?.text ?? "") + (ev.delta ?? "") });
        }
        break;
      }
      case "response.output_audio.delta": {
        const pcm = base64ToPcm16(ev.delta ?? "");
        responseAudio.get(ev.response_id)?.push(pcm);
        if (!silencedResponses.has(ev.response_id)) playChunk(pcm);
        break;
      }
      case "response.done": {
        responseActive = false;
        const rid = ev.response?.id;
        if (rid === activeResponseId) activeResponseId = null;
        silencedResponses.delete(rid);
        const id = responseTurns.get(rid);
        if (id) {
          const chunks = responseAudio.get(rid) ?? [];
          const patch: Partial<Turn> = { pending: false, cancelled: ev.response?.status === "cancelled" };
          if (chunks.length) {
            const pcm = concatPcm16(chunks);
            patch.audioUrl = URL.createObjectURL(encodeWav(pcm, outputRate));
            patch.audioSeconds = pcm.length / outputRate;
          }
          updateTurn(id, patch);
        }
        responseTurns.delete(rid);
        responseAudio.delete(rid);
        break;
      }
      case "error":
        addError(ev.error?.message ?? JSON.stringify(ev));
        break;
    }
  }

  // ---- talking ------------------------------------------------------------

  /** Stop whatever the assistant is saying so the user can speak. */
  function interrupt() {
    stopPlayback();
    if (activeResponseId) silencedResponses.add(activeResponseId);
    if (responseActive) send({ type: "response.cancel" });
  }

  function requestResponse() {
    send({ type: "response.create" });
  }

  function sendText() {
    const text = textInput.trim();
    if (!text || !isConnected) return;
    interrupt();
    send({ type: "conversation.item.create", item: { type: "message", role: "user", content: [{ type: "input_text", text }] } });
    addTurn({ id: nextTurnId(), role: "user", text });
    textInput = "";
    requestResponse();
  }

  // ---- live status in the user's bubble -------------------------------------

  // performance.now() when the current quiet stretch began, or null while
  // speaking. Set from the ~40ms audio blocks; the time-left line is redrawn
  // every animation frame from it so it drains smoothly.
  let quietSince: number | null = null;
  // Share of the pause left before the turn is sent: 1 while speaking,
  // falling to 0 as the quiet stretch reaches "Pause before sending".
  let sendLeft = $state(1);
  let waitingToSend = $derived(sendLeft < 1);

  $effect(() => {
    if (!capturing) return;
    let frame = requestAnimationFrame(function tickStatus(now) {
      // Push-to-talk sends on release, so there is no pause to show.
      sendLeft =
        $micModeStore === "ptt" || quietSince === null
          ? 1
          : Math.max(0, 1 - (now - quietSince) / $vadPauseStore);
      frame = requestAnimationFrame(tickStatus);
    });
    return () => cancelAnimationFrame(frame);
  });

  // ---- waveform of the turn being recorded ----------------------------------

  // One bar per ~40ms audio block, newest on the right: about 2.5s of history.
  const WAVE_BARS = 64;
  let waveLevels: number[] = [];
  let waveEl = $state<HTMLCanvasElement | null>(null);

  function drawWave() {
    const el = waveEl;
    const ctx = el?.getContext("2d");
    if (!el || !ctx) return;
    const dpr = window.devicePixelRatio || 1;
    const w = el.clientWidth;
    const h = el.clientHeight;
    if (el.width !== Math.round(w * dpr) || el.height !== Math.round(h * dpr)) {
      el.width = Math.round(w * dpr);
      el.height = Math.round(h * dpr);
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);
    // Drawn in the canvas's text color, so it follows the theme.
    ctx.fillStyle = getComputedStyle(el).color;
    const step = w / WAVE_BARS;
    const barW = Math.max(1, step * 0.6);
    const offset = WAVE_BARS - waveLevels.length;
    waveLevels.forEach((level, i) => {
      // sqrt lifts quiet speech so it is still visible next to loud speech
      const barH = Math.max(1, Math.sqrt(level) * h);
      ctx.fillRect((offset + i) * step, (h - barH) / 2, barW, barH);
    });
  }

  function beginUtterance() {
    interrupt();
    waveLevels = [];
    quietSince = null;
    sendLeft = 1;
    capturing = true;
    sentChunks = [];
    // Hands-free mode starts with what was heard just before speech was detected.
    for (const chunk of preroll) appendAudio(chunk);
    preroll = [];
    prerollLen = 0;
  }

  function appendAudio(pcm: Int16Array) {
    sentChunks.push(pcm);
    send({ type: "input_audio_buffer.append", audio: pcm16ToBase64(pcm) });
  }

  function endUtterance(discard = false) {
    if (!capturing) return;
    capturing = false;
    const pcm = concatPcm16(sentChunks);
    sentChunks = [];
    // Under ~100ms is a key tap, not a question.
    if (discard || pcm.length < inputRate / 10) {
      send({ type: "input_audio_buffer.clear" });
      return;
    }
    const id = nextTurnId();
    addTurn({
      id,
      role: "user",
      text: "",
      audioUrl: URL.createObjectURL(encodeWav(pcm, inputRate)),
      audioSeconds: pcm.length / inputRate,
    });
    pendingUserTurn = id;
    send({ type: "input_audio_buffer.commit" });
    requestResponse();
  }

  // ---- microphone ---------------------------------------------------------

  // An AudioWorklet that forwards raw input frames to the main thread. Loaded
  // from a blob URL so it ships inside the UI bundle.
  const workletSource = `
    class Capture extends AudioWorkletProcessor {
      process(inputs) {
        const ch = inputs[0] && inputs[0][0];
        if (ch) this.port.postMessage(ch.slice(0));
        return true;
      }
    }
    registerProcessor("realtime-capture", Capture);
  `;

  // True while the mic is being opened (permission prompt, worklet load), so
  // the UI can already show the listening state instead of flashing back to
  // the "Turn on mic" button.
  let micStarting = $state(false);

  async function startMic() {
    if (micOn || micStarting) return;
    micStarting = true;
    try {
      await openMic();
    } finally {
      micStarting = false;
    }
  }

  async function openMic() {
    if (!navigator.mediaDevices?.getUserMedia) {
      addError(
        window.isSecureContext
          ? "microphone unavailable: this browser does not support getUserMedia"
          : `microphone unavailable: browsers only allow the microphone on https or localhost, not ${window.location.origin}`
      );
      return;
    }
    try {
      micStream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, channelCount: 1 },
      });
    } catch (err) {
      addError(`microphone unavailable: ${err instanceof Error ? err.message : String(err)}`);
      return;
    }
    micCtx = new AudioContext();
    const url = URL.createObjectURL(new Blob([workletSource], { type: "application/javascript" }));
    try {
      await micCtx.audioWorklet.addModule(url);
      const source = micCtx.createMediaStreamSource(micStream);
      micNode = new AudioWorkletNode(micCtx, "realtime-capture");
      micNode.port.onmessage = (e) => onMicFrames(e.data as Float32Array);
      source.connect(micNode);
    } catch (err) {
      // Release the mic rather than leave it recording with nothing reading it.
      stopMic();
      addError(`microphone unavailable: ${err instanceof Error ? err.message : String(err)}`);
      return;
    } finally {
      URL.revokeObjectURL(url);
    }
    vad.reset();
    micOn = true;
    // Disconnected while the mic was opening: don't leave it recording.
    if (!ws) stopMic();
  }

  function stopMic() {
    if (capturing) endUtterance(true);
    micNode?.disconnect();
    micNode = null;
    micStream?.getTracks().forEach((t) => t.stop());
    micStream = null;
    micCtx?.close();
    micCtx = null;
    micResampler = null;
    micBlock = [];
    micBlockLen = 0;
    preroll = [];
    prerollLen = 0;
    micOn = false;
    micLevel = 0;
  }

  function onMicFrames(frames: Float32Array) {
    if (!micCtx) return;
    micBlock.push(frames);
    micBlockLen += frames.length;
    // Work in ~40ms blocks: small enough to feel live, large enough that the
    // websocket is not flooded with tiny appends.
    const blockSize = Math.round(micCtx.sampleRate * 0.04);
    if (micBlockLen < blockSize) return;

    const block = new Float32Array(micBlockLen);
    let offset = 0;
    for (const f of micBlock) {
      block.set(f, offset);
      offset += f.length;
    }
    micBlock = [];
    micBlockLen = 0;

    const level = rms(block);
    micLevel = Math.min(1, level * 8);
    if (micResampler?.fromRate !== micCtx.sampleRate || micResampler.toRate !== inputRate) {
      micResampler = new Resampler(micCtx.sampleRate, inputRate);
    }
    const pcm = floatToPcm16(micResampler.process(block));
    const blockMs = (block.length / micCtx.sampleRate) * 1000;

    if ($micModeStore === "vad") {
      // Echo cancellation is imperfect, so the assistant's own voice coming
      // back through the speakers must be louder to count as barging in.
      vad.threshold = assistantSpeaking ? $vadThresholdStore * 3 : $vadThresholdStore;
      vad.silenceLimitMs = $vadPauseStore;
      vad.minSpeechMs = $vadMinSpeechStore;
      const result = vad.feed(level, blockMs);
      quietSince = vad.quietMs > 0 ? performance.now() - vad.quietMs : null;
      if (result === "start") beginUtterance();
      else if (result === "end") endUtterance();
      else if (result === "discard") endUtterance(true);
    }

    if (capturing) {
      appendAudio(pcm);
      waveLevels.push(micLevel);
      if (waveLevels.length > WAVE_BARS) waveLevels.shift();
      drawWave();
    } else {
      preroll.push(pcm);
      prerollLen += pcm.length;
      while (preroll.length > 1 && prerollLen - preroll[0].length > (inputRate * $vadPrerollStore) / 1000) {
        prerollLen -= preroll.shift()!.length;
      }
    }
  }

  async function pttDown() {
    if (!isConnected || $micModeStore !== "ptt" || capturing) return;
    pttHeld = true;
    await startMic();
    // Released (or disconnected) while the mic was opening: no turn.
    if (!micOn || !pttHeld || capturing) return;
    // Push-to-talk starts clean: no audio from before the button was pressed.
    preroll = [];
    prerollLen = 0;
    beginUtterance();
  }

  function pttUp() {
    pttHeld = false;
    if ($micModeStore !== "ptt" || !capturing) return;
    endUtterance();
  }

  async function toggleHandsFree() {
    if (micOn) stopMic();
    else await startMic();
  }

  function setMicMode(mode: MicMode) {
    if (mode === $micModeStore) return;
    stopMic();
    micModeStore.set(mode);
  }

  function onWindowKey(e: KeyboardEvent) {
    // Space is push-to-talk when focus is not in a text field.
    if (e.code !== "Space" || !isConnected || $micModeStore !== "ptt") return;
    // The tab stays mounted while hidden; only act while it is on screen.
    if (!transcriptEl?.offsetParent) return;
    const target = e.target as HTMLElement | null;
    if (target && (target.closest("input, textarea, select, [contenteditable=true], button, [role=combobox]"))) return;
    if (!target?.closest("[data-realtime-root]") && target !== document.body) return;
    e.preventDefault();
    if (e.type === "keydown" && !e.repeat) pttDown();
    if (e.type === "keyup") pttUp();
  }

  // ---- playback -----------------------------------------------------------

  function playChunk(pcm: Int16Array) {
    if (pcm.length === 0) return;
    if (!playCtx) {
      playCtx = new AudioContext();
      playHead = 0;
    }
    const buffer = playCtx.createBuffer(1, pcm.length, outputRate);
    buffer.copyToChannel(pcm16ToFloat(pcm) as Float32Array<ArrayBuffer>, 0);
    const src = playCtx.createBufferSource();
    src.buffer = buffer;
    src.connect(playCtx.destination);
    // A small lead on the first chunk absorbs network jitter.
    const startAt = Math.max(playCtx.currentTime + 0.05, playHead);
    src.start(startAt);
    playHead = startAt + buffer.duration;
    playSources.add(src);
    assistantSpeaking = true;
    src.onended = () => {
      playSources.delete(src);
      if (playSources.size === 0) assistantSpeaking = false;
    };
  }

  function stopPlayback() {
    for (const src of playSources) {
      src.onended = null;
      try {
        src.stop();
      } catch {
        // already stopped
      }
    }
    playSources.clear();
    playHead = 0;
    assistantSpeaking = false;
  }

  // ---- replay / download ---------------------------------------------------

  function stopReplay() {
    replayEl?.pause();
    replayEl = null;
    replayingId = null;
  }

  function toggleReplay(turn: Turn) {
    const wasPlaying = replayingId === turn.id;
    stopReplay();
    if (wasPlaying || !turn.audioUrl) return;
    const el = new Audio(turn.audioUrl);
    el.onended = () => {
      if (replayEl === el) stopReplay();
    };
    replayEl = el;
    replayingId = turn.id;
    el.play().catch(() => {
      if (replayEl === el) stopReplay();
    });
  }

  function downloadTurn(turn: Turn) {
    if (!turn.audioUrl) return;
    const a = document.createElement("a");
    a.href = turn.audioUrl;
    a.download = `realtime-${turn.role}-${new Date().toISOString().replace(/[:.]/g, "-").slice(0, -5)}.wav`;
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  function resetVadSettings() {
    vadThresholdStore.set(VAD_DEFAULTS.threshold);
    vadPauseStore.set(VAD_DEFAULTS.pauseMs);
    vadMinSpeechStore.set(VAD_DEFAULTS.minSpeechMs);
    vadPrerollStore.set(VAD_DEFAULTS.prerollMs);
  }

  function clearTranscript() {
    stopReplay();
    for (const t of turns) if (t.audioUrl) URL.revokeObjectURL(t.audioUrl);
    turns = [];
    events = [];
  }

  // Close everything if the Playground unmounts mid-session.
  $effect(() => () => {
    stopReplay();
    disconnect();
    playCtx?.close();
    playCtx = null;
    for (const t of turns) if (t.audioUrl) URL.revokeObjectURL(t.audioUrl);
  });

  function handleTextKeyDown(event: KeyboardEvent) {
    if (isSubmitEnter(event)) {
      event.preventDefault();
      sendText();
    }
  }
</script>

<svelte:window onkeydown={onWindowKey} onkeyup={onWindowKey} />

<!-- Connect lives in the transcript area; once connecting, the status line
     carries the Cancel / Disconnect button instead. -->
{#snippet connectButton(size: "default" | "lg")}
  <!-- Stays in place while connecting, so the middle of the screen does not
       swap to a message and back. -->
  <Button
    class={size === "lg" ? "h-11 px-6 text-base shadow-sm" : "shadow-sm"}
    onclick={connect}
    disabled={!$selectedModelStore || status === "connecting"}
  >
    {#if status === "connecting"}
      <LoaderCircle class="animate-spin" />
      Connecting
    {:else}
      <Phone />
      Connect
    {/if}
  </Button>
{/snippet}

<div class="flex flex-col h-full gap-3" data-realtime-root>
  <!-- Model and settings -->
  <div class="shrink-0 flex flex-wrap gap-2">
    <ModelSelector
      bind:value={$selectedModelStore}
      placeholder="Select a realtime model..."
      disabled={status !== "idle" && status !== "error"}
      match={{ inputModalities: ["audio"], outputModalities: ["audio"] }}
    />
    <Button variant="outline" size="icon" onclick={() => (showSettings = true)} title="Realtime settings">
      <Settings />
    </Button>
  </div>

  <!-- Settings dialog. The prompt is sent in session.update right after
       connecting, and the server refuses instruction changes once the
       conversation has started, so it is locked while connected. -->
  <Dialog.Root bind:open={showSettings}>
    <Dialog.Content class="top-8 translate-y-0 max-w-xl max-h-[calc(100dvh-2rem)] overflow-y-auto">
      <Dialog.Header>
        <Dialog.Title>Realtime Settings</Dialog.Title>
      </Dialog.Header>

      {#if status === "connecting" || isConnected}
        <p class="text-sm text-muted-foreground">Disconnect to change the prompt, it applies when a session starts.</p>
      {/if}

      <div class="space-y-4">
        <div>
          <div class="mb-1 flex items-center justify-between">
            <Label for="realtime-system-prompt">System Prompt</Label>
            <Button
              variant="link"
              size="sm"
              class="h-auto p-0 text-xs"
              onclick={() => systemPromptStore.set(DEFAULT_SYSTEM_PROMPT)}
              disabled={status === "connecting" || isConnected || $systemPromptStore === DEFAULT_SYSTEM_PROMPT}
            >Reset to default</Button>
          </div>
          <Textarea
            id="realtime-system-prompt"
            class="resize-y min-h-24 max-h-[50dvh]"
            placeholder="No system prompt"
            rows={6}
            bind:value={$systemPromptStore}
            disabled={status === "connecting" || isConnected}
          />
        </div>

        <div class="space-y-3 border-t pt-4">
          <div class="flex items-center justify-between">
            <div>
              <h4 class="text-sm font-medium">Hands-free voice detection</h4>
              <p class="text-muted-foreground text-xs">How the mic decides when you start and stop talking. Changes apply right away.</p>
            </div>
            <Button
              variant="link"
              size="sm"
              class="h-auto p-0 text-xs shrink-0"
              onclick={resetVadSettings}
              disabled={$vadThresholdStore === VAD_DEFAULTS.threshold &&
                $vadPauseStore === VAD_DEFAULTS.pauseMs &&
                $vadMinSpeechStore === VAD_DEFAULTS.minSpeechMs &&
                $vadPrerollStore === VAD_DEFAULTS.prerollMs}
            >Reset to defaults</Button>
          </div>

          <div>
            <Label class="mb-1" for="realtime-vad-sensitivity">Sensitivity</Label>
            <input
              id="realtime-vad-sensitivity"
              type="range"
              min="0.005"
              max="0.1"
              step="0.005"
              class="accent-primary w-full"
              value={0.105 - $vadThresholdStore}
              oninput={(e) => vadThresholdStore.set(+(0.105 - Number((e.currentTarget as HTMLInputElement).value)).toFixed(3))}
            />
            <p class="text-muted-foreground text-xs">
              How loud the mic must be to count as speech. Raise it if quiet words get cut off, lower it if background
              noise starts a turn or keeps one from ending.
            </p>
            {#if micOn}
              <!-- Live level against the threshold, on the same scale as the
                   meter by the mic button. -->
              <div class="relative mt-2 h-2 rounded bg-muted overflow-hidden" title="Microphone level, the line is the threshold">
                <div class="h-full bg-green-500 transition-[width] duration-75" style="width: {Math.round(micLevel * 100)}%"></div>
                <div class="absolute inset-y-0 w-0.5 bg-foreground" style="left: {Math.min(100, $vadThresholdStore * 800)}%"></div>
              </div>
            {/if}
          </div>

          <div>
            <Label class="mb-1" for="realtime-vad-pause">Pause before sending: {($vadPauseStore / 1000).toFixed(1)}s</Label>
            <input id="realtime-vad-pause" type="range" min="300" max="3000" step="100" class="accent-primary w-full" bind:value={$vadPauseStore} />
            <p class="text-muted-foreground text-xs">
              How long you must be quiet before your turn is sent. Longer lets you pause mid-sentence, shorter gets
              faster replies.
            </p>
          </div>

          <div>
            <Label class="mb-1" for="realtime-vad-min">Minimum speech: {$vadMinSpeechStore}ms</Label>
            <input id="realtime-vad-min" type="range" min="50" max="1000" step="50" class="accent-primary w-full" bind:value={$vadMinSpeechStore} />
            <p class="text-muted-foreground text-xs">Sounds shorter than this, like a cough or a click, are ignored.</p>
          </div>

          <div>
            <Label class="mb-1" for="realtime-vad-preroll">Lead-in: {$vadPrerollStore}ms</Label>
            <input id="realtime-vad-preroll" type="range" min="0" max="1000" step="50" class="accent-primary w-full" bind:value={$vadPrerollStore} />
            <p class="text-muted-foreground text-xs">
              Audio kept from just before speech is detected, so your first word is not clipped.
            </p>
          </div>
        </div>
      </div>

      <Dialog.Footer>
        <Button variant="outline" onclick={() => (showSettings = false)}>Done</Button>
      </Dialog.Footer>
    </Dialog.Content>
  </Dialog.Root>

  {#if !$hasListedModels}
    <EmptyState message="No models configured. Add a model whose server supports /v1/realtime." />
  {:else}
    <!-- Status line -->
    <div class="shrink-0 flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
      <span class="flex items-center gap-1.5">
        <span
          class="inline-block size-2 rounded-full {isConnected
            ? 'bg-green-500'
            : status === 'connecting'
              ? 'bg-yellow-500 animate-pulse'
              : status === 'error'
                ? 'bg-red-500'
                : 'bg-muted-foreground/40'}"
        ></span>
        {isConnected ? "connected" : status === "connecting" ? "connecting" : status === "error" ? "disconnected" : "not connected"}
      </span>
      {#if statusDetail}
        <span class={status === "error" ? "text-red-500" : ""}>{statusDetail}</span>
      {/if}
      {#if capturing}
        <span class="flex items-center gap-1.5 text-red-500 font-medium">
          ● listening
          <canvas bind:this={waveEl} class="h-5 w-32" aria-label="Recording waveform"></canvas>
        </span>
      {:else if responseActive && !assistantSpeaking}
        <span>thinking…</span>
      {/if}
      {#if assistantSpeaking}
        <span class="text-primary font-medium">speaking…</span>
      {/if}
      <span class="flex-1"></span>
      {#if status === "connecting" || isConnected}
        <Button variant="destructive" size="sm" onclick={disconnect}>
          <PhoneOff />
          {status === "connecting" ? "Cancel" : "Disconnect"}
        </Button>
      {/if}
      <Button variant="ghost" size="sm" onclick={clearTranscript} disabled={turns.length === 0 && events.length === 0} title="Clear transcript">
        <Trash2 />
      </Button>
    </div>

    <!-- Transcript -->
    <div bind:this={transcriptEl} class="flex-1 min-h-0 overflow-y-auto rounded-md border border-border bg-background p-3 space-y-3">
      {#if turns.length === 0 && !capturing}
        <div class="h-full flex flex-col items-center justify-center gap-4 text-center text-muted-foreground text-sm px-4">
          {#if isConnected && $micModeStore === "ptt"}
            <p>Hold the mic button (or the space bar) and talk, or type a message below.</p>
          {:else if isConnected && (micOn || micStarting)}
            <p>Listening, start talking. A pause ends your turn.</p>
          {:else if isConnected}
            <Button class="h-11 px-6 text-base shadow-sm" onclick={toggleHandsFree}>
              <Mic />
              Turn on mic
            </Button>
          {:else if $selectedModelStore}
            {@render connectButton("lg")}
          {:else}
            <p>Pick a model whose server supports /v1/realtime.</p>
          {/if}
        </div>
      {/if}
      {#each turns as turn (turn.id)}
        {#if turn.role === "error"}
          <div class="text-sm text-red-500 border border-red-500/30 rounded-md px-3 py-2">{turn.text}</div>
        {:else}
          <div class="flex {turn.role === 'user' ? 'justify-end' : 'justify-start'}">
            <div
              class="max-w-[85%] rounded-lg px-3 py-2 text-sm {turn.role === 'user'
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted'}"
            >
              {#if turn.text}
                <p class="whitespace-pre-wrap break-words">{turn.text}</p>
              {:else if turn.pending}
                <p class="opacity-60">…</p>
              {:else if turn.role === "user" && turn.audioUrl}
                <p class="opacity-80 italic">voice message</p>
              {/if}
              {#if turn.audioUrl}
                <div class="mt-1 -mb-0.5 flex items-center gap-0.5 opacity-80">
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 rounded px-1 py-0.5 text-xs hover:bg-black/10 dark:hover:bg-white/10"
                    onclick={() => toggleReplay(turn)}
                    title={replayingId === turn.id ? "Stop" : "Replay"}
                  >
                    {#if replayingId === turn.id}<Square class="size-3.5" />{:else}<Play class="size-3.5" />{/if}
                    {turn.audioSeconds?.toFixed(1)}s
                  </button>
                  <button
                    type="button"
                    class="inline-flex items-center rounded p-1 hover:bg-black/10 dark:hover:bg-white/10"
                    onclick={() => downloadTurn(turn)}
                    title="Download audio"
                  >
                    <Download class="size-3.5" />
                  </button>
                </div>
              {/if}
              {#if turn.cancelled}
                <p class="text-xs opacity-60 mt-1">interrupted</p>
              {/if}
            </div>
          </div>
        {/if}
      {/each}
      {#if capturing}
        <!-- The turn being recorded; replaced by the finished turn once sent. -->
        <div class="flex justify-end">
          <div class="max-w-[85%] rounded-lg px-3 py-2 text-sm bg-primary/70 text-primary-foreground">
            <!-- Fixed width so nothing moves while the line drains. -->
            <div class="w-32 space-y-1.5">
              <p class="flex items-center gap-1.5">
                <span
                  class="inline-block size-2 rounded-full transition-colors {waitingToSend
                    ? 'bg-amber-400'
                    : 'bg-red-500 animate-pulse'}"
                ></span>
                listening
              </p>
              <!-- Time left before the turn is sent: full while speaking,
                   emptying toward the right through the pause. -->
              <div class="h-1 rounded-full bg-primary-foreground/20 overflow-hidden" title="Time left before sending">
                <div
                  class="h-full w-full rounded-full origin-right {waitingToSend ? 'bg-amber-400' : 'bg-primary-foreground/80'}"
                  style="transform: scaleX({sendLeft})"
                ></div>
              </div>
            </div>
          </div>
        </div>
      {/if}
      {#if turns.length > 0 && $selectedModelStore && !isConnected}
        <div class="sticky bottom-0 flex justify-center pt-2">
          {@render connectButton("default")}
        </div>
      {/if}
    </div>

    <!-- Controls -->
    <div class="shrink-0 flex flex-col gap-2">
      <div class="flex flex-wrap items-center gap-2">
        <div class="inline-flex rounded-md border border-border p-0.5 text-sm">
          <button
            type="button"
            class="px-2.5 py-1 rounded {$micModeStore === 'ptt' ? 'bg-muted font-medium' : 'text-muted-foreground'}"
            onclick={() => setMicMode("ptt")}
          >Push to talk</button>
          <button
            type="button"
            class="px-2.5 py-1 rounded {$micModeStore === 'vad' ? 'bg-muted font-medium' : 'text-muted-foreground'}"
            onclick={() => setMicMode("vad")}
          >Hands-free</button>
        </div>

        {#if $micModeStore === "ptt"}
          <Button
            class="select-none touch-none {capturing ? 'bg-red-600 hover:bg-red-600 text-white' : ''}"
            disabled={!isConnected}
            onpointerdown={(e: PointerEvent) => {
              (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
              pttDown();
            }}
            onpointerup={pttUp}
            onpointercancel={pttUp}
            title="Hold to talk (or hold the space bar)"
          >
            <Mic />
            {capturing ? "Release to send" : "Hold to talk"}
          </Button>
        {:else}
          <Button
            variant={micOn ? "destructive" : "default"}
            disabled={!isConnected}
            onclick={toggleHandsFree}
          >
            {#if micOn}<MicOff /> Mic off{:else}<Mic /> Mic on{/if}
          </Button>
        {/if}

        {#if micOn}
          <div class="h-2 w-24 rounded bg-muted overflow-hidden" title="Microphone level">
            <div class="h-full bg-green-500 transition-[width] duration-75" style="width: {Math.round(micLevel * 100)}%"></div>
          </div>
        {/if}
      </div>

      <div class="flex gap-2">
        <input
          class="flex-1 min-w-0 h-9 rounded-md border border-border bg-background px-3 text-sm disabled:opacity-60"
          placeholder={isConnected ? "Or type a message..." : "Connect to start"}
          bind:value={textInput}
          onkeydown={handleTextKeyDown}
          disabled={!isConnected}
        />
        <Button onclick={sendText} disabled={!isConnected || !textInput.trim()} title="Send">
          <Send />
        </Button>
      </div>

      <details class="text-xs">
        <summary class="cursor-pointer text-muted-foreground select-none">Events ({events.length})</summary>
        <div class="mt-1 max-h-48 overflow-y-auto rounded border border-border bg-background p-2 font-mono space-y-0.5">
          {#each events as ev, i (i)}
            <div class="truncate" title={ev.body}>
              <span class="text-muted-foreground">{ev.time}</span>
              <span class={ev.dir === "out" ? "text-blue-500" : "text-green-600"}>{ev.dir === "out" ? "→" : "←"}</span>
              <span class="font-medium">{ev.type}</span>
              <span class="text-muted-foreground">{ev.body}</span>
            </div>
          {/each}
        </div>
      </details>
    </div>
  {/if}
</div>
