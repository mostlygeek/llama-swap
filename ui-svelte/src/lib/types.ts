export type ConnectionState = "connected" | "connecting" | "disconnected";

export type ModelStatus = "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown";

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
  aliases?: string[];
  capabilities?: ModelCapabilities;
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
  type: "modelStatus" | "logData" | "activity" | "inflight" | "uiConfig" | "perfsys" | "perfgpu";
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

export interface ChatMessage {
  role: "user" | "assistant" | "system";
  content: string | ContentPart[];
  reasoning_content?: string;
  reasoningTimeMs?: number;
}

export function getTextContent(content: string | ContentPart[]): string {
  if (typeof content === "string") {
    return content;
  }
  const textParts = content.filter((part): part is TextContentPart => part.type === "text");
  return textParts.map((part) => part.text).join("\n");
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
