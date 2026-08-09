import type { VideoGenerationParams, VideoJob } from "./types";
import { playgroundSessionHeaders } from "./playgroundSession";

const TERMINAL_FAILURE_STATUSES = new Set(["failed", "error", "cancelled", "canceled"]);
const DEFAULT_POLL_INTERVAL_MS = 2000;
const DEFAULT_POLL_TIMEOUT_MS = 10 * 60 * 1000;

function buildVideoFormData(
  model: string,
  prompt: string,
  params: VideoGenerationParams,
  referenceFile?: File | null
): FormData {
  const formData = new FormData();
  formData.append("model", model);
  formData.append("prompt", prompt);
  if (params.size) formData.append("size", params.size);
  if (params.seconds !== undefined) formData.append("seconds", String(params.seconds));
  if (params.fps !== undefined) formData.append("fps", String(params.fps));
  if (params.negativePrompt) formData.append("negative_prompt", params.negativePrompt);
  if (referenceFile) formData.append("input_reference", referenceFile);

  // Backend-specific extension fields (e.g. vLLM-omni's width/height/
  // num_frames/seed/extra_params - see the "vllm-omni extension fields"
  // table on https://docs.vllm.ai/projects/vllm-omni/en/latest/serving/videos_api/).
  // Scalars are sent as plain strings; objects/arrays (like extra_params)
  // are JSON-encoded, matching what the docs' curl examples send. Applied
  // last so it intentionally overrides the basic fields above, and "model"
  // is never overridable since it drives llama-swap's routing.
  if (params.advanced) {
    for (const [key, value] of Object.entries(params.advanced)) {
      if (key === "model" || value === undefined) continue;
      formData.set(key, typeof value === "object" && value !== null ? JSON.stringify(value) : String(value));
    }
  }

  return formData;
}

async function postVideoForm(path: string, formData: FormData, signal?: AbortSignal): Promise<Response> {
  const response = await fetch(path, {
    method: "POST",
    headers: playgroundSessionHeaders,
    body: formData,
    signal,
  });
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Video API error: ${response.status} - ${errorText}`);
  }
  return response;
}

/** POST /v1/videos - creates an async video generation job. */
export async function createVideoJob(
  model: string,
  prompt: string,
  params: VideoGenerationParams,
  referenceFile?: File | null,
  signal?: AbortSignal
): Promise<VideoJob> {
  const response = await postVideoForm("/v1/videos", buildVideoFormData(model, prompt, params, referenceFile), signal);
  return response.json();
}

/** POST /v1/videos/sync - blocks until the video is generated. */
export async function createVideoSync(
  model: string,
  prompt: string,
  params: VideoGenerationParams,
  referenceFile?: File | null,
  signal?: AbortSignal
): Promise<Blob> {
  const response = await postVideoForm("/v1/videos/sync", buildVideoFormData(model, prompt, params, referenceFile), signal);
  return response.blob();
}

// GET/DELETE /v1/videos/{id}... routes carry no model in the request body, so
// llama-swap dispatches them via a required ?model= query param (same
// convention as /props).

export async function getVideoStatus(id: string, model: string, signal?: AbortSignal): Promise<VideoJob> {
  const url = `/v1/videos/${encodeURIComponent(id)}?model=${encodeURIComponent(model)}`;
  const response = await fetch(url, { headers: playgroundSessionHeaders, signal, cache: "no-store" });
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Video API error: ${response.status} - ${errorText}`);
  }
  return response.json();
}

export async function getVideoContent(id: string, model: string, signal?: AbortSignal): Promise<Blob> {
  const url = `/v1/videos/${encodeURIComponent(id)}/content?model=${encodeURIComponent(model)}`;
  const response = await fetch(url, { headers: playgroundSessionHeaders, signal });
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Video API error: ${response.status} - ${errorText}`);
  }
  return response.blob();
}

export async function deleteVideo(id: string, model: string, signal?: AbortSignal): Promise<void> {
  const url = `/v1/videos/${encodeURIComponent(id)}?model=${encodeURIComponent(model)}`;
  const response = await fetch(url, { method: "DELETE", headers: playgroundSessionHeaders, signal });
  if (!response.ok) {
    const errorText = await response.text();
    throw new Error(`Video API error: ${response.status} - ${errorText}`);
  }
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const timer = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        reject(new DOMException("Aborted", "AbortError"));
      },
      { once: true }
    );
  });
}

function formatJobError(error: VideoJob["error"]): string {
  if (!error) return "";
  return typeof error === "string" ? error : (error.message ?? JSON.stringify(error));
}

/**
 * Creates an async video job (POST /v1/videos) and polls it to completion,
 * returning the rendered video bytes. onStatus is invoked after job creation
 * and after every poll so callers can render progress. The vLLM-omni docs
 * only confirm "queued" and "completed" status values, so polling treats
 * "completed" as success, a small set of known failure strings as terminal
 * failure, and anything else as still in progress.
 */
export async function generateVideoAsync(
  model: string,
  prompt: string,
  params: VideoGenerationParams,
  referenceFile: File | null | undefined,
  signal: AbortSignal,
  onStatus?: (job: VideoJob) => void,
  options?: { pollIntervalMs?: number; timeoutMs?: number }
): Promise<Blob> {
  const pollIntervalMs = options?.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS;
  const timeoutMs = options?.timeoutMs ?? DEFAULT_POLL_TIMEOUT_MS;

  let current = await createVideoJob(model, prompt, params, referenceFile, signal);
  onStatus?.(current);

  const deadline = Date.now() + timeoutMs;
  while (current.status !== "completed") {
    if (TERMINAL_FAILURE_STATUSES.has(current.status)) {
      const detail = formatJobError(current.error);
      throw new Error(`Video generation ${current.status}${detail ? `: ${detail}` : ""}`);
    }
    if (Date.now() > deadline) {
      throw new Error("Timed out waiting for video generation to complete");
    }
    await sleep(pollIntervalMs, signal);
    current = await getVideoStatus(current.id, model, signal);
    onStatus?.(current);
  }

  return getVideoContent(current.id, model, signal);
}
