// API data stores, the SSE event stream, and REST helpers. Ported from stores/api.ts.
// The live model list arrives via the SSE "modelStatus" event; the playground
// list is fetched from /v1/models (fetchPlaygroundModels).
import { observable, derived } from "./store.js";
import { connectionState } from "./theme.js";
import { playgroundStores } from "./playgroundActivity.js";

const LOG_LENGTH_LIMIT = 1024 * 100; // 100KB of log data

export const models = observable([]);
export const playgroundModels = observable([]);
export const hasListedModels = derived([playgroundModels], (list) => list.length > 0);
export const proxyLogs = observable("");
export const upstreamLogs = observable("");
// metrics is kept for backwards compatibility with the stats page: it is no
// longer pushed over SSE; the Activity page fetches /api/metrics/activity.
export const metrics = observable([]);
export const activityRevision = observable(0);
export const inFlightRequests = observable(0);
export const inflightRequestEntries = observable([]);
export const profiles = observable([]);
export const activeProfile = observable(null);
export const uiConfig = observable({ activity: { session_id: [] } });
export const performanceEnabled = observable(false);
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
    activityRevision.set(0);
    inFlightRequests.set(0);
    inflightRequestEntries.set([]);
    playgroundModels.set([]);
    profiles.set([]);
    activeProfile.set(null);
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
      models.set([]);
      playgroundModels.set([]);
      activityRevision.update((n) => n + 1);
      inFlightRequests.set(0);
      inflightRequestEntries.set([]);
      profiles.set([]);
      activeProfile.set(null);
      retryCount = 0;
      connectionState.set("connected");
      fetchProfiles().catch((error) => console.error(error));
    };

    apiEventSource.onmessage = (e) => {
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

export function handleAPIEventMessage(data) {
  const message = JSON.parse(data);
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
    case "activity": {
      // The server bumps a revision instead of pushing metrics; pages fetch
      // /api/metrics/activity when it changes.
      activityRevision.update((n) => n + 1);
      break;
    }
    case "inflight": {
      const stats = JSON.parse(message.data);
      inflightRequestEntries.update((current) => {
        let requests = current;
        switch (stats.operation) {
          case "snapshot":
            requests = stats.requests ?? [];
            break;
          case "upsert": {
            if (!stats.request) break;
            const received = stats.request;
            const index = current.findIndex((request) => request.id === received.id);
            requests =
              index === -1
                ? [...current, received]
                : current.map((request, i) => (i === index ? received : request));
            break;
          }
          case "remove":
            requests = current.filter((request) => request.id !== stats.id);
            break;
        }
        requests = [...requests].sort((a, b) => {
          const byTime = Date.parse(a.timestamp) - Date.parse(b.timestamp);
          return byTime || a.id.localeCompare(b.id, undefined, { numeric: true });
        });
        inFlightRequests.set(requests.length);
        return requests;
      });
      break;
    }
    case "uiConfig": {
      uiConfig.set(JSON.parse(message.data));
      break;
    }
    case "profileChanged": {
      const state = JSON.parse(message.data);
      activeProfile.set(state.active);
      fetchPlaygroundModels().catch((err) => console.error(err));
      break;
    }
  }
}

// ── Profiles ─────────────────────────────────────────────────────────────────

export async function fetchProfiles() {
  const response = await fetch("/api/profiles");
  if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
  const state = await response.json();
  profiles.set(state.profiles ?? []);
  activeProfile.set(state.active ?? null);
  return state;
}

export async function setActiveProfile(name) {
  const response = await fetch("/api/profiles/active", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ profile: name ?? "" }),
  });
  if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
  // The profileChanged SSE event refreshes the stores server-side; also set
  // locally so the UI reacts instantly.
  activeProfile.set(name);
  await fetchPlaygroundModels().catch((err) => console.error(err));
}

// ── Models ───────────────────────────────────────────────────────────────────

let playgroundModelsFetch = null;
let playgroundModelsRequest = 0;
let playgroundModelsRefreshQueued = false;

async function loadPlaygroundModels(request) {
  try {
    const response = await fetch("/v1/models");
    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
    const responseData = await response.json();
    const records = responseData.data ?? [];
    const aliasesByModel = new Map();
    for (const record of records) {
      const metadata = record.meta?.llamaswap;
      if (metadata?.aliases) {
        aliasesByModel.set(record.id, new Set(metadata.aliases));
      }
      if (metadata?.type === "alias" && metadata.modelID) {
        const aliases = aliasesByModel.get(metadata.modelID) ?? new Set();
        aliases.add(record.id);
        aliasesByModel.set(metadata.modelID, aliases);
      }
    }
    const newModels = records
      .filter((record) => record.meta?.llamaswap?.type !== "alias")
      .map((record) => {
        const metadata = record.meta?.llamaswap;
        const metadataType = metadata?.type;
        const playgroundType =
          metadataType && metadataType !== "alias" ? metadataType : "model";
        return {
          id: record.id,
          state: "unknown",
          name: record.name ?? "",
          description: record.description ?? "",
          unlisted: false,
          peerID: metadata?.peerID ?? "",
          playgroundType,
          aliases: [...(aliasesByModel.get(record.id) ?? [])],
          capabilities: record.capabilities,
          context_length: record.context_length,
          strategy: metadata?.strategy,
          targets: metadata?.targets ?? [],
          spillover: metadata?.spillover,
        };
      });
    newModels.sort((a, b) =>
      (a.name + a.id).localeCompare(b.name + b.id, undefined, { numeric: true })
    );
    if (request === playgroundModelsRequest) playgroundModels.set(newModels);
    return newModels;
  } catch (error) {
    console.error("Failed to fetch Playground models:", error);
    return [];
  }
}

export function fetchPlaygroundModels() {
  if (playgroundModelsFetch) {
    playgroundModelsRefreshQueued = true;
    return playgroundModelsFetch;
  }
  const request = ++playgroundModelsRequest;
  const currentFetch = loadPlaygroundModels(request).finally(() => {
    if (playgroundModelsFetch !== currentFetch) return;
    playgroundModelsFetch = null;
    if (playgroundModelsRefreshQueued) {
      playgroundModelsRefreshQueued = false;
      void fetchPlaygroundModels();
    }
  });
  playgroundModelsFetch = currentFetch;
  return currentFetch;
}

// ── Activity ─────────────────────────────────────────────────────────────────

export async function getActivity(params = {}) {
  const query = new URLSearchParams();
  if (params.model) query.set("model", params.model);
  if (params.page) query.set("page", String(params.page));
  if (params.limit) query.set("limit", String(params.limit));
  if (params.sort) query.set("sort", params.sort);
  if (params.order) query.set("order", params.order);
  if (params.minID) query.set("min_id", String(params.minID));
  if (params.maxID) query.set("max_id", String(params.maxID));
  const url = query.size > 0 ? `/api/metrics/activity?${query}` : "/api/metrics/activity";
  const response = await fetch(url);
  if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
  return await response.json();
}

export async function getActivityStats(model) {
  const query = new URLSearchParams();
  if (model) query.set("model", model);
  const url = query.size > 0 ? `/api/metrics/stats?${query}` : "/api/metrics/stats";
  const response = await fetch(url);
  if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
  return await response.json();
}

// ── Ops ──────────────────────────────────────────────────────────────────────

export async function unloadAllModels() {
  const response = await fetch(`/api/models/unload`, { method: "POST" });
  if (!response.ok) throw new Error(`Failed to unload models: ${response.status}`);
}

export async function unloadSingleModel(model) {
  const response = await fetch(`/api/models/unload/${model}`, { method: "POST" });
  if (!response.ok) throw new Error(`Failed to unload model: ${response.status}`);
}

export async function cancelInflightRequest(id) {
  const response = await fetch(`/api/inflight/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
  });
  if (!response.ok) throw new Error(`Failed to cancel request: ${response.status}`);
}

export async function loadModel(model, signal) {
  const response = await fetch(`/upstream/${model}/?_=${Date.now()}`, { method: "GET", signal });
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

export async function checkPerformanceEnabled() {
  try {
    const response = await fetch("/api/performance");
    // 200 = enabled, 503 = monitor not available
    performanceEnabled.set(response.ok);
  } catch {
    performanceEnabled.set(false);
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

export async function getHardware() {
  const response = await fetch("/api/hardware");
  if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`);
  return await response.json();
}

// re-export so callers can import activity flags from one place
export { playgroundStores };
