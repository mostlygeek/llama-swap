export type ConnectionState = "connected" | "connecting" | "disconnected";

export type ModelStatus = "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown";
export type PlaygroundModelType = "model" | "peer" | "selector" | "profile";

export interface ModelCapabilities {
  vision?: boolean;
  audio_transcriptions?: boolean;
  audio_speech?: boolean;
  image_generation?: boolean;
  image_to_image?: boolean;
  function_calling?: boolean;
  reranker?: boolean;
}

export interface Model {
  id: string;
  state: ModelStatus;
  name: string;
  description: string;
  unlisted: boolean;
  peerID: string;
  playgroundType?: PlaygroundModelType;
  aliases?: string[];
  capabilities?: ModelCapabilities;
  context_length?: number;
  // selector-only fields from the v1/models llamaswap metadata
  strategy?: string;
  targets?: string[];
  spillover?: number;
}

export interface Profile {
  id: string;
  description: string;
  pins: Record<string, string>;
}

export interface ProfileState {
  active: string | null;
  profiles: Profile[];
}

export interface TokenMetrics {
  cache_tokens: number;
  draft_tokens: number;
  draft_acc_tokens: number;
  input_tokens: number;
  output_tokens: number;
  prompt_per_second: number;
  tokens_per_second: number;
}

export interface ActivityLogEntry {
  id: number;
  timestamp: string;
  src: string;
  model: string;
  req_path: string;
  resp_content_type: string;
  resp_status_code: number;
  tokens: TokenMetrics;
  duration_ms: number;
  has_capture: boolean;
  error_msg?: string;
  metadata?: Record<string, string>;
}

export interface TailcatStatus {
  enabled: boolean;
  address: string;
  models: string[];
}

export interface ActivityPage {
  data: ActivityLogEntry[];
  page: number;
  limit: number;
  total: number;
  total_pages: number;
}

export interface ReqRespCapture {
  id: number;
  req_path: string;
  req_headers: Record<string, string>;
  req_body: string; // base64 encoded bytes
  resp_headers: Record<string, string>;
  resp_body: string; // base64 encoded bytes
}

export interface LogData {
  source: "upstream" | "proxy";
  data: string;
}

export interface InflightRequestEntry {
  id: string;
  timestamp: string;
  model: string;
  req_path: string;
  method: string;
  req_headers: Record<string, string>;
  remote_ip: string;
  resp_headers: Record<string, string>;
  resp_bytes: number;
  elapsed_ms: number;
  client_received_at_ms?: number;
  metadata?: Record<string, string>;
}

export interface InFlightStats {
  operation: "snapshot" | "upsert" | "remove";
  requests?: InflightRequestEntry[];
  request?: InflightRequestEntry;
  id?: string;
}

export interface UIConfig {
  activity: {
    session_id: string[];
  };
}

export interface NetIOStat {
  name: string;
  bytes_recv: number;
  bytes_sent: number;
}

export interface SysStat {
  timestamp: string;
  cpu_util_per_core: number[];
  mem_total_mb: number;
  mem_used_mb: number;
  mem_free_mb: number;
  swap_total_mb: number;
  swap_used_mb: number;
  load_avg_1: number;
  load_avg_5: number;
  load_avg_15: number;
  net_io: NetIOStat[];
}

export interface GpuStat {
  timestamp: string;
  id: number;
  name: string;
  uuid: string;
  temp_c: number;
  vram_temp_c: number;
  gpu_util_pct: number;
  mem_util_pct: number;
  mem_used_mb: number;
  mem_total_mb: number;
  fan_speed_pct: number;
  power_draw_w: number;
}

export interface PerformanceResponse {
  sys_stats: SysStat[];
  gpu_stats: GpuStat[];
}

export interface APIEventEnvelope {
  type: "modelStatus" | "logData" | "activity" | "inflight" | "uiConfig" | "profileChanged" | "perfsys" | "perfgpu";
  data: string;
}

export interface HistogramData {
  bins: number[];
  min: number;
  max: number;
  binSize: number;
  p99: number;
  p95: number;
  p50: number;
}

export interface ActivityStatsData {
  total_requests: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cache_tokens: number;
  prompt_histogram: HistogramData | null;
  gen_histogram: HistogramData | null;
}

export interface VersionInfo {
  build_date: string;
  commit: string;
  version: string;
}

export interface HardwareSnapshot {
  schema_version: number;
  captured_at: string;
  capture: HardwareCapture;
  architecture: HardwareArchitecture;
  operating_system: HardwareOperatingSystem;
  environment: HardwareEnvironment;
  cpu: HardwareCPU;
  memory: HardwareMemory;
  accelerators: HardwareAccelerator[];
}

export interface HardwareCapture {
  scope: "inference_host";
  method: "detected" | "detected_and_edited" | "manual";
  detector: { name: string; version: string } | null;
}

export interface HardwareArchitecture {
  name: string;
  raw_name?: string | null;
}

export interface HardwareOperatingSystem {
  family: string;
  name: string | null;
  version: string | null;
  kernel: string | null;
  raw_family?: string | null;
}

export interface HardwareEnvironment {
  kind: string;
  name: string | null;
  version: string | null;
  raw_kind?: string | null;
}

export interface HardwareCPU {
  vendor: string | null;
  model: string | null;
  socket_count: number | null;
  physical_core_count: number | null;
  logical_thread_count: number | null;
}

export interface HardwareMemory {
  capacity_bytes: number;
}

export interface HardwareAccelerator {
  index: number;
  kind: "gpu" | "npu" | "other";
  raw_kind?: string | null;
  vendor: string | null;
  model: string | null;
  architecture: string | null;
  memory: {
    kind: "dedicated" | "unified" | "shared_system" | "unknown";
    capacity_bytes: number | null;
  };
  driver: { name: string | null; version: string | null } | null;
  power_limit_watts: number | null;
}

export type ScreenWidth = "xs" | "sm" | "md" | "lg" | "xl" | "2xl";

export type TextContentPart = {
  type: "text";
  text: string;
};

export type ImageContentPart = {
  type: "image_url";
  image_url: { url: string };
};

export type ContentPart = TextContentPart | ImageContentPart;

export type ChatRole = "user" | "assistant" | "system" | "tool";

/** A tool call requested by the model. `arguments` is a JSON *string*. */
export interface ToolCall {
  id: string;
  type: "function";
  function: { name: string; arguments: string };
}

/** Token count and duration of one phase of a turn. */
export interface PhaseStats {
  /** Tokens in this phase. Absent when unknown. */
  tokens?: number;
  /** Time spent in this phase. */
  ms?: number;
  /**
   * Throughput. Usually tokens / ms, but for the prompt only the non-cached
   * tokens count: the cached ones took no time.
   */
  perSecond?: number;
  /**
   * True when `tokens` was not reported by the backend: it is a count of
   * streamed chunks, or the backend total split between thinking and answer
   * by chunk ratio.
   */
  approxTokens: boolean;
  /** True when `ms` is wall-clock time measured in the browser. */
  approxTimings: boolean;
}

/**
 * Per-turn generation stats shown with a chat message. Counts and durations
 * come from the backend when it reports usage/timings; until then they are
 * client-side measurements, flagged so the UI can mark them approximate.
 */
export interface GenerationStats {
  /** Prompt processing. Its token count is backend-reported only. */
  prompt: PhaseStats;
  /** Everything generated: thinking plus answer. */
  generation: PhaseStats;
  /**
   * Only when the turn streamed reasoning. The backend reports one total, so
   * the two are split by streamed chunks and the boundary is the first answer
   * token; `answer` is absent while the model is still thinking.
   */
  reasoning?: PhaseStats;
  answer?: PhaseStats;

  /** Prompt tokens served from the KV cache; backend-reported only. */
  cachedTokens?: number;
  /** Speculative decoding / MTP draft counts; backend-reported only. */
  draftTokens?: number;
  draftAccepted?: number;
  /** Request start to first streamed token, measured in the browser. */
  firstTokenMs?: number;
  /** Request start to last streamed token, measured in the browser. */
  wallMs?: number;
  /** The backend's finish_reason: "stop", "length", "tool_calls", ... */
  finishReason?: string;
  /**
   * Context window of the model that served the turn, captured when the turn
   * started so the number stays put when a different model is selected later.
   */
  contextLength?: number;
}

export interface ChatMessage {
  role: ChatRole;
  content: string | ContentPart[];
  reasoning_content?: string;
  reasoningTimeMs?: number;
  /** UI-only. Stats for the request that produced this assistant turn. */
  stats?: GenerationStats;

  /** Wire fields. tool_calls is assistant-only; the rest are tool-only. */
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  name?: string;

  /** UI-only. Stripped before a message is sent upstream. */
  toolOk?: boolean;
  toolDurationMs?: number;
}

export function getTextContent(content: string | ContentPart[]): string {
  if (typeof content === "string") {
    return content;
  }
  const textParts = content.filter((part): part is TextContentPart => part.type === "text");
  return textParts.map((part) => part.text).join("\n");
}

/**
 * True when an assistant turn carries nothing worth rendering as a message.
 *
 * A turn that only requested tools has no text: it is shown as its tool cards
 * instead of an empty bubble. A turn that *thought* before calling the tool is
 * not empty, though -- the reasoning is the only record of why the model chose
 * that call, and hiding it makes a reasoning model look like it thinks only on
 * its final answer.
 */
export function isToolCallOnlyTurn(message: ChatMessage): boolean {
  return (
    message.role === "assistant" &&
    Boolean(message.tool_calls?.length) &&
    getTextContent(message.content) === "" &&
    !message.reasoning_content
  );
}

export function getImageUrls(content: string | ContentPart[]): string[] {
  if (typeof content === "string") {
    return [];
  }
  return content
    .filter((part): part is ImageContentPart => part.type === "image_url")
    .map((part) => part.image_url.url);
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  stream: boolean;
  temperature?: number;
  max_tokens?: number;
}

export interface ImageGenerationRequest {
  model: string;
  prompt: string;
  n?: number;
  size?: string;
}

export interface ImageGenerationResponse {
  created: number;
  data: Array<{
    url?: string;
    b64_json?: string;
  }>;
}

// SDAPI types (stable-diffusion.cpp)
export type ImageApiMode = "openai" | "sdapi";

export interface SdApiLora {
  name: string;
  path: string;
}

export interface SdApiLoraRef {
  path: string;
  multiplier: number;
}

export interface SdApiTxt2ImgRequest {
  model?: string;
  prompt: string;
  negative_prompt?: string;
  width?: number;
  height?: number;
  steps?: number;
  cfg_scale?: number;
  seed?: number;
  batch_size?: number;
  sampler_name?: string;
  scheduler?: string;
  lora?: SdApiLoraRef[];
}

export interface SdApiResponse {
  images: string[];
  parameters: Record<string, unknown>;
  info: string;
}

export interface AudioTranscriptionRequest {
  file: File;
  model: string;
}

export interface AudioTranscriptionResponse {
  text: string;
}

export interface SpeechGenerationRequest {
  model: string;
  input: string;
  voice: string;
}
