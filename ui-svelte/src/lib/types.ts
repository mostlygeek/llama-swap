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
  has_capture: boolean;
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

export type BenchyJobStatus = "running" | "done" | "error" | "canceled";

export type BenchyIntelligencePlugin =
  | "all"
  | "core6"
  | "mmlu"
  | "arc-c"
  | "hellaswag"
  | "winogrande"
  | "gsm8k"
  | "truthfulqa"
  | "ifeval"
  | "evalplus"
  | "terminal_bench"
  | "aider"
  | "swebench_verified";

export interface BenchyStartOptions {
  queueModels?: string[];
  baseUrl?: string;
  tokenizer?: string;
  pp?: number[];
  tg?: number[];
  depth?: number[];
  concurrency?: number[];
  runs?: number;
  latencyMode?: "api" | "generation" | "none";
  noCache?: boolean;
  noWarmup?: boolean;
  adaptPrompt?: boolean;
  enablePrefixCaching?: boolean;
  trustRemoteCode?: boolean;
  enableIntelligence?: boolean;
  intelligencePlugins?: BenchyIntelligencePlugin[];
  allowCodeExec?: boolean;
  datasetCacheDir?: string;
  outputDir?: string;
  maxConcurrent?: number;
}

export interface BenchyJob {
  id: string;
  model: string;
  queueModels?: string[];
  queueCurrentIndex?: number;
  queueCurrentModel?: string;
  queueCompletedCount?: number;
  tokenizer: string;
  baseUrl: string;
  pp: number[];
  tg: number[];
  depth?: number[];
  concurrency?: number[];
  runs: number;
  latencyMode?: "api" | "generation" | "none";
  noCache?: boolean;
  noWarmup?: boolean;
  adaptPrompt?: boolean;
  enablePrefixCaching?: boolean;
  trustRemoteCode?: boolean;
  enableIntelligence?: boolean;
  intelligencePlugins?: BenchyIntelligencePlugin[];
  allowCodeExec?: boolean;
  datasetCacheDir?: string;
  outputDir?: string;
  maxConcurrent?: number;
  status: BenchyJobStatus;
  startedAt: string;
  finishedAt?: string;
  exitCode?: number;
  stdout?: string;
  stderr?: string;
  error?: string;
}

export interface BenchyStartResponse {
  id: string;
}

export interface RecipeCatalogItem {
  id: string;
  ref: string;
  path: string;
  name: string;
  description: string;
  model: string;
  soloOnly: boolean;
  clusterOnly: boolean;
  defaultTensorParallel: number;
}

export interface RecipeManagedModel {
  modelId: string;
  recipeRef: string;
  name: string;
  description: string;
  aliases: string[];
  useModelName: string;
  mode: "solo" | "cluster";
  tensorParallel: number;
  nodes?: string;
  extraArgs?: string;
  group: string;
  unlisted?: boolean;
  managed: boolean;
  benchyTrustRemoteCode?: boolean;
}

export interface RecipeUIState {
  configPath: string;
  backendDir: string;
  recipes: RecipeCatalogItem[];
  models: RecipeManagedModel[];
  groups: string[];
}

export type RecipeBackendSource = "override" | "env" | "default";

export interface RecipeBackendState {
  backendDir: string;
  backendSource: RecipeBackendSource;
  options: string[];
}

export type RecipeBackendAction = "git_pull" | "git_pull_rebase" | "build_vllm" | "build_mxfp4";

export interface RecipeBackendActionResponse {
  action: RecipeBackendAction | string;
  backendDir: string;
  command: string;
  message: string;
  output?: string;
  durationMs: number;
}

export interface RecipeUpsertRequest {
  modelId: string;
  recipeRef: string;
  name?: string;
  description?: string;
  aliases?: string[];
  useModelName?: string;
  mode?: "solo" | "cluster";
  tensorParallel?: number;
  nodes?: string;
  extraArgs?: string;
  group?: string;
  unlisted?: boolean;
  benchyTrustRemoteCode?: boolean;
}

export interface ConfigEditorState {
  path: string;
  content: string;
  updatedAt?: string;
}

export type ClusterOverallStatus = "healthy" | "degraded" | "solo" | "error";

export interface ClusterNodeStatus {
  ip: string;
  isLocal: boolean;
  port22Open: boolean;
  port22LatencyMs?: number;
  sshOk: boolean;
  sshLatencyMs?: number;
  error?: string;
  dgx?: ClusterDGXStatus;
}

export interface ClusterStoragePathPresence {
  path: string;
  exists: boolean;
  error?: string;
}

export interface ClusterStorageNodeState {
  ip: string;
  isLocal: boolean;
  presentCount: number;
  paths: ClusterStoragePathPresence[];
}

export interface ClusterStorageState {
  paths: string[];
  nodes: ClusterStorageNodeState[];
  duplicatePaths?: string[];
  sharedAllPaths?: string[];
  note: string;
}

export interface ClusterStatusState {
  backendDir: string;
  autodiscoverPath: string;
  detectedAt: string;
  localIp: string;
  cidr: string;
  ethIf: string;
  ibIf: string;
  nodeCount: number;
  remoteCount: number;
  reachableBySsh: number;
  overall: ClusterOverallStatus;
  summary: string;
  errors?: string[];
  nodes: ClusterNodeStatus[];
  storage?: ClusterStorageState;
}

export interface ClusterDGXStatus {
  supported: boolean;
  checkedAt: string;
  updateAvailable?: boolean;
  rebootRunning?: boolean;
  upgradeProgress?: number;
  upgradeStatus?: string;
  cacheProgress?: number;
  cacheStatus?: string;
  error?: string;
}

export interface ClusterDGXUpdateNodeResult {
  ip: string;
  isLocal: boolean;
  ok: boolean;
  durationMs: number;
  output?: string;
  error?: string;
}

export interface ClusterDGXUpdateResponse {
  action: string;
  startedAt: string;
  completedAt: string;
  success: number;
  failed: number;
  results: ClusterDGXUpdateNodeResult[];
}

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
