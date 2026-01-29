export type ConnectionState = "connected" | "connecting" | "disconnected";

export type ModelStatus = "ready" | "starting" | "stopping" | "stopped" | "shutdown" | "unknown";

export interface Model {
  id: string;
  state: ModelStatus;
  name: string;
  description: string;
  unlisted: boolean;
  peerID: string;
}

export interface Metrics {
  id: number;
  timestamp: string;
  model: string;
  cache_tokens: number;
  input_tokens: number;
  output_tokens: number;
  prompt_per_second: number;
  tokens_per_second: number;
  duration_ms: number;
}

export interface LogData {
  source: "upstream" | "proxy";
  data: string;
}

export interface APIEventEnvelope {
  type: "modelStatus" | "logData" | "metrics";
  data: string;
}

export interface VersionInfo {
  build_date: string;
  commit: string;
  version: string;
}

export type ScreenWidth = "xs" | "sm" | "md" | "lg" | "xl" | "2xl";
