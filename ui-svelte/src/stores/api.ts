import { writable, derived } from "svelte/store";
import type {
  Model,
  ActivityPage,
  ActivityStatsData,
  VersionInfo,
  LogData,
  APIEventEnvelope,
  ReqRespCapture,
  InFlightStats,
  InflightRequestEntry,
  PerformanceResponse,
} from "../lib/types";
import { connectionState } from "./theme";

const LOG_LENGTH_LIMIT = 1024 * 100; /* 100KB of log data */

// Stores
export const models = writable<Model[]>([]);

// True when at least one listed (non-unlisted) model is configured.
export const hasListedModels = derived(models, ($models) => $models.some((m) => !m.unlisted));
export const proxyLogs = writable<string>("");
export const upstreamLogs = writable<string>("");
export const activityRevision = writable<number>(0);
export const inFlightRequests = writable<number>(0);
export const inflightRequestEntries = writable<InflightRequestEntry[]>([]);
export const performanceEnabled = writable<boolean>(false);
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
    activityRevision.set(0);
    inFlightRequests.set(0);
    inflightRequestEntries.set([]);
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
      activityRevision.update((n) => n + 1);
      inFlightRequests.set(0);
      inflightRequestEntries.set([]);
      models.set([]);
      retryCount = 0;
      connectionState.set("connected");
    };

    apiEventSource.onmessage = (e: MessageEvent) => {
      try {
        handleAPIEventMessage(e.data);
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

export function handleAPIEventMessage(data: string): void {
  const message = JSON.parse(data) as APIEventEnvelope;
  switch (message.type) {
    case "modelStatus": {
      const newModels = JSON.parse(message.data) as Model[];
      // Sort models by name and id
      newModels.sort((a, b) => {
        return (a.name + a.id).localeCompare(b.name + b.id, undefined, { numeric: true });
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
      // Backward-compatible activity invalidation for older servers.
      activityRevision.update((n) => n + 1);
      break;
    }

    case "activity": {
      activityRevision.update((n) => n + 1);
      break;
    }

    case "inflight": {
      const stats = JSON.parse(message.data) as InFlightStats;
      const requests = stats.requests ?? [];
      inFlightRequests.set(requests.length);
      inflightRequestEntries.set(requests);
      break;
    }
  }
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

export async function getActivity(params: {
  model?: string;
  page?: number;
  limit?: number;
  sort?: string;
  order?: "asc" | "desc";
} = {}): Promise<ActivityPage> {
  const query = new URLSearchParams();
  if (params.model) query.set("model", params.model);
  if (params.page) query.set("page", String(params.page));
  if (params.limit) query.set("limit", String(params.limit));
  if (params.sort) query.set("sort", params.sort);
  if (params.order) query.set("order", params.order);
  const url = query.size > 0 ? `/api/metrics/activity?${query}` : "/api/metrics/activity";

  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`Failed to fetch activity: ${response.status}`);
  }
  return await response.json();
}

export async function getActivityStats(model?: string): Promise<ActivityStatsData> {
  const query = new URLSearchParams();
  if (model) query.set("model", model);
  const url = query.size > 0 ? `/api/metrics/stats?${query}` : "/api/metrics/stats";

  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`Failed to fetch activity stats: ${response.status}`);
  }
  return await response.json();
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

export async function cancelInflightRequest(id: string): Promise<void> {
  try {
    const response = await fetch(`/api/inflight/${encodeURIComponent(id)}/cancel`, {
      method: "POST",
    });
    if (!response.ok) {
      throw new Error(`Failed to cancel request: ${response.status}`);
    }
  } catch (error) {
    console.error("Failed to cancel inflight request", id, error);
    throw error;
  }
}

export async function loadModel(model: string, signal?: AbortSignal): Promise<void> {
  try {
    const response = await fetch(`/upstream/${model}/?_=${Date.now()}`, {
      method: "GET",
      signal,
    });
    if (!response.ok) {
      throw new Error(`Failed to load model: ${response.status}`);
    }
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      return;
    }
    console.error("Failed to load model:", error);
    throw error;
  }
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

export async function checkPerformanceEnabled(): Promise<void> {
  try {
    const response = await fetch("/api/performance");
    if (!response.ok) {
      performanceEnabled.set(false);
      return;
    }
    const data = await response.json();
    performanceEnabled.set(data.enabled);
  } catch {
    performanceEnabled.set(false);
  }
}

export async function fetchPerformance(after?: string): Promise<PerformanceResponse | null> {
  try {
    const url = after ? `/api/performance?after=${encodeURIComponent(after)}` : "/api/performance";
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch performance data:", error);
    return null;
  }
}
