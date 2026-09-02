// API data stores, the SSE event stream, and REST helpers. Ported from stores/api.ts.
// Note: stores/api.ts's listModels() (GET /api/models/) was dead code — that route
// does not exist; the live model list arrives via the SSE "modelStatus" event.
import { observable } from "./store.js";
import { connectionState } from "./theme.js";
import { playgroundStores } from "./playgroundActivity.js";

const LOG_LENGTH_LIMIT = 1024 * 100; // 100KB of log data

export const models = observable([]);
export const proxyLogs = observable("");
export const upstreamLogs = observable("");
export const metrics = observable([]);
export const inFlightRequests = observable(0);
export const versionInfo = observable({
  build_date: "unknown",
  commit: "unknown",
  version: "unknown",
});

let apiEventSource = null;

function appendLog(newData, store) {
  store.update((prev) => {
    const updated = prev + newData;
    return updated.length > LOG_LENGTH_LIMIT ? updated.slice(-LOG_LENGTH_LIMIT) : updated;
  });
}

export function enableAPIEvents(enabled) {
  if (!enabled) {
    apiEventSource?.close();
    apiEventSource = null;
    metrics.set([]);
    inFlightRequests.set(0);
    return;
  }

  let retryCount = 0;
  const initialDelay = 1000;

  const connect = () => {
    apiEventSource?.close();
    apiEventSource = new EventSource("/api/events");

    connectionState.set("connecting");

    apiEventSource.onopen = () => {
      proxyLogs.set("");
      upstreamLogs.set("");
      metrics.set([]);
      inFlightRequests.set(0);
      models.set([]);
      retryCount = 0;
      connectionState.set("connected");
    };

    apiEventSource.onmessage = (e) => {
      try {
        const message = JSON.parse(e.data);
        switch (message.type) {
          case "modelStatus": {
            const newModels = JSON.parse(message.data);
            newModels.sort((a, b) =>
              (a.name + a.id).localeCompare(b.name + b.id, undefined, { numeric: true })
            );
            models.set(newModels);
            break;
          }
          case "logData": {
            const logData = JSON.parse(message.data);
            if (logData.source === "proxy") appendLog(logData.data, proxyLogs);
            else if (logData.source === "upstream") appendLog(logData.data, upstreamLogs);
            break;
          }
          case "metrics": {
            const newMetrics = JSON.parse(message.data);
            metrics.update((prev) => [...newMetrics, ...prev]);
            break;
          }
          case "inflight": {
            const stats = JSON.parse(message.data);
            inFlightRequests.set(stats.total ?? 0);
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
      if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
      versionInfo.set(await response.json());
    } catch (error) {
      console.error(error);
    }
  }
});

export async function unloadAllModels() {
  const response = await fetch(`/api/models/unload`, { method: "POST" });
  if (!response.ok) throw new Error(`Failed to unload models: ${response.status}`);
}

export async function unloadSingleModel(model) {
  const response = await fetch(`/api/models/unload/${model}`, { method: "POST" });
  if (!response.ok) throw new Error(`Failed to unload model: ${response.status}`);
}

export async function loadModel(model) {
  const response = await fetch(`/upstream/${model}/`, { method: "GET" });
  if (!response.ok) throw new Error(`Failed to load model: ${response.status}`);
}

export async function getCapture(id) {
  try {
    const response = await fetch(`/api/captures/${id}`);
    if (response.status === 404) return null;
    if (!response.ok) throw new Error(`Failed to fetch capture: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch capture:", error);
    return null;
  }
}

export async function fetchPerformance(after) {
  try {
    const url = after ? `/api/performance?after=${encodeURIComponent(after)}` : "/api/performance";
    const response = await fetch(url);
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    return await response.json();
  } catch (error) {
    console.error("Failed to fetch performance data:", error);
    return null;
  }
}

// re-export so callers can import activity flags from one place
export { playgroundStores };
