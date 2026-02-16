import { writable } from "svelte/store";
import type {
  Model,
  Metrics,
  VersionInfo,
  LogData,
  APIEventEnvelope,
  ReqRespCapture,
  BenchyJob,
  BenchyStartResponse,
  BenchyStartOptions,
  RecipeUIState,
  RecipeUpsertRequest,
  ConfigEditorState,
} from "../lib/types";
import { connectionState } from "./theme";

const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

// Stores
export const models = writable<Model[]>([]);
export const proxyLogs = writable<string>("");
export const upstreamLogs = writable<string>("");
export const metrics = writable<Metrics[]>([]);
export const versionInfo = writable<VersionInfo>({
  build_date: "unknown",
  commit: "unknown",
  version: "unknown",
});

let apiEventSource: EventSource | null = null;

function appendLog(newData: string, store: typeof proxyLogs | typeof upstreamLogs): void {
  store.update((prev) => {
    const updatedLog = prev + newData;
    return updatedLog.length > LOG_LENGTH_LIMIT ? updatedLog.slice(-LOG_LENGTH_LIMIT) : updatedLog;
  });
}

export function enableAPIEvents(enabled: boolean): void {
  if (!enabled) {
    apiEventSource?.close();
    apiEventSource = null;
    metrics.set([]);
    return;
  }

  let retryCount = 0;
  const initialDelay = 1000; // 1 second

  const connect = () => {
    apiEventSource?.close();
    apiEventSource = new EventSource("/api/events");

    connectionState.set("connecting");

    apiEventSource.onopen = () => {
      // Clear everything on connect to keep things in sync
      proxyLogs.set("");
      upstreamLogs.set("");
      metrics.set([]);
      models.set([]);
      retryCount = 0;
      connectionState.set("connected");
    };

    apiEventSource.onmessage = (e: MessageEvent) => {
      try {
        const message = JSON.parse(e.data) as APIEventEnvelope;
        switch (message.type) {
          case "modelStatus": {
            const newModels = JSON.parse(message.data) as Model[];
            // Sort models by name and id
            newModels.sort((a, b) => {
              return (a.name + a.id).localeCompare(b.name + b.id);
            });
            models.set(newModels);
            break;
          }

          case "logData": {
            const logData = JSON.parse(message.data) as LogData;
            switch (logData.source) {
              case "proxy":
                appendLog(logData.data, proxyLogs);
                break;
              case "upstream":
                appendLog(logData.data, upstreamLogs);
                break;
            }
            break;
          }

          case "metrics": {
            const newMetrics = JSON.parse(message.data) as Metrics[];
            metrics.update((prevMetrics) => [...newMetrics, ...prevMetrics]);
            break;
          }
        }
      } catch (err) {
        console.error(e.data, err);
      }
    };

    apiEventSource.onerror = () => {
      apiEventSource?.close();
      retryCount++;
      const delay = Math.min(initialDelay * Math.pow(2, retryCount - 1), 5000);
      connectionState.set("disconnected");
      setTimeout(connect, delay);
    };
  };

  connect();
}

// Fetch version info when connected
connectionState.subscribe(async (status) => {
  if (status === "connected") {
    try {
      const response = await fetch("/api/version");
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const data: VersionInfo = await response.json();
      versionInfo.set(data);
    } catch (error) {
      console.error(error);
    }
  }
});

export async function listModels(): Promise<Model[]> {
  try {
    const response = await fetch("/api/models/");
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    const data = await response.json();
    return data || [];
  } catch (error) {
    console.error("Failed to fetch models:", error);
    return [];
  }
}

export async function unloadAllModels(): Promise<void> {
  try {
    const response = await fetch(`/api/models/unload`, {
      method: "POST",
    });
    if (!response.ok) {
      throw new Error(`Failed to unload models: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to unload models:", error);
    throw error;
  }
}

export async function stopClusterAndUnload(): Promise<void> {
  try {
    const response = await fetch(`/api/cluster/stop`, {
      method: "POST",
    });
    if (!response.ok) {
      const msg = await response.text().catch(() => "");
      throw new Error(msg || `Failed to stop cluster: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to stop cluster:", error);
    throw error;
  }
}

export async function unloadSingleModel(model: string): Promise<void> {
  try {
    const response = await fetch(`/api/models/unload/${model}`, {
      method: "POST",
    });
    if (!response.ok) {
      throw new Error(`Failed to unload model: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to unload model", model, error);
    throw error;
  }
}

export async function loadModel(model: string): Promise<void> {
  try {
    const response = await fetch(`/upstream/${model}/`, {
      method: "GET",
    });
    if (!response.ok) {
      throw new Error(`Failed to load model: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to load model:", error);
    throw error;
  }
}

export async function startBenchy(model: string, opts: BenchyStartOptions = {}): Promise<string> {
  const payload = {
    model,
    baseUrl: opts.baseUrl,
    tokenizer: opts.tokenizer,
    pp: opts.pp,
    tg: opts.tg,
    depth: opts.depth,
    concurrency: opts.concurrency,
    runs: opts.runs,
    latencyMode: opts.latencyMode,
    noCache: opts.noCache,
    noWarmup: opts.noWarmup,
    adaptPrompt: opts.adaptPrompt,
    enablePrefixCaching: opts.enablePrefixCaching,
    trustRemoteCode: opts.trustRemoteCode,
    enableIntelligence: opts.enableIntelligence,
    intelligencePlugins: opts.intelligencePlugins,
    allowCodeExec: opts.allowCodeExec,
    datasetCacheDir: opts.datasetCacheDir,
    outputDir: opts.outputDir,
    maxConcurrent: opts.maxConcurrent,
  };

  const response = await fetch(`/api/benchy`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to start benchy: ${response.status}`);
  }

  const data: BenchyStartResponse = await response.json();
  if (!data?.id) {
    throw new Error("Invalid benchy start response");
  }
  return data.id;
}

export async function getBenchyJob(id: string): Promise<BenchyJob> {
  const response = await fetch(`/api/benchy/${id}`);
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to fetch benchy job: ${response.status}`);
  }
  return (await response.json()) as BenchyJob;
}

export async function cancelBenchyJob(id: string): Promise<void> {
  const response = await fetch(`/api/benchy/${id}/cancel`, {
    method: "POST",
  });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to cancel benchy job: ${response.status}`);
  }
}

export async function getRecipeUIState(signal?: AbortSignal): Promise<RecipeUIState> {
  const response = await fetch(`/api/recipes/state`, { signal });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to fetch recipe state: ${response.status}`);
  }
  return (await response.json()) as RecipeUIState;
}

export async function upsertRecipeModel(payload: RecipeUpsertRequest): Promise<RecipeUIState> {
  const response = await fetch(`/api/recipes/models`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to save recipe model: ${response.status}`);
  }
  return (await response.json()) as RecipeUIState;
}

export async function deleteRecipeModel(modelId: string): Promise<RecipeUIState> {
  const response = await fetch(`/api/recipes/models/${encodeURIComponent(modelId)}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to delete recipe model: ${response.status}`);
  }
  return (await response.json()) as RecipeUIState;
}

export async function getConfigEditorState(signal?: AbortSignal): Promise<ConfigEditorState> {
  const response = await fetch(`/api/config/editor`, { signal });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to fetch config editor state: ${response.status}`);
  }
  return (await response.json()) as ConfigEditorState;
}

export async function saveConfigEditorContent(content: string): Promise<ConfigEditorState> {
  const response = await fetch(`/api/config/editor`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ content }),
  });
  if (!response.ok) {
    const msg = await response.text().catch(() => "");
    throw new Error(msg || `Failed to save config: ${response.status}`);
  }
  return (await response.json()) as ConfigEditorState;
}

export async function getCapture(id: number): Promise<ReqRespCapture | null> {
  try {
    const response = await fetch(`/api/captures/${id}`);
    if (response.status === 404) {
      return null;
    }
    if (!response.ok) {
      throw new Error(`Failed to fetch capture: ${response.status}`);
    }
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch capture:", error);
    return null;
  }
}
