import type { MantleTask, HFModel, LocalModel, BackendEntry } from "../lib/types";

// --- HF Model search ---

export async function searchHFModels(query: string, limit = 20): Promise<HFModel[]> {
  try {
    const res = await fetch(`/api/mantle/models/search?q=${encodeURIComponent(query)}&limit=${limit}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("HF search failed:", e);
    return [];
  }
}

export async function listHFFiles(modelID: string): Promise<string[]> {
  try {
    const res = await fetch(`/api/mantle/models/files?model=${encodeURIComponent(modelID)}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("HF files list failed:", e);
    return [];
  }
}

// --- Downloads ---

export async function startDownload(modelID: string, filename: string): Promise<MantleTask | null> {
  try {
    const res = await fetch("/api/mantle/models/download", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ modelID, filename }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("Start download failed:", e);
    return null;
  }
}

export async function cancelDownload(taskID: string): Promise<boolean> {
  try {
    const res = await fetch(`/api/mantle/models/download/${taskID}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

// --- Local models ---

export async function listLocalModels(): Promise<LocalModel[]> {
  try {
    const res = await fetch("/api/mantle/models/local");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("List local models failed:", e);
    return [];
  }
}

export async function deleteLocalModel(name: string): Promise<boolean> {
  try {
    const res = await fetch(`/api/mantle/models/local/${encodeURIComponent(name)}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

// --- Config ---

export async function getConfig(): Promise<string | null> {
  try {
    const res = await fetch("/api/mantle/config");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.text();
  } catch (e) {
    console.error("Get config failed:", e);
    return null;
  }
}

export async function putConfig(yaml: string): Promise<boolean> {
  try {
    const res = await fetch("/api/mantle/config", {
      method: "PUT",
      headers: { "Content-Type": "text/yaml" },
      body: yaml,
    });
    return res.ok;
  } catch {
    return false;
  }
}

// --- Backend builds ---

export async function startBuild(repo: string, branch: string, cmakeFlags = "", backendName = ""): Promise<MantleTask | null> {
  try {
    const res = await fetch("/api/mantle/backends/build", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ repo, branch, cmakeFlags, backendName }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("Start build failed:", e);
    return null;
  }
}

export async function cancelBuild(taskID: string): Promise<boolean> {
  try {
    const res = await fetch(`/api/mantle/backends/build/${taskID}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

// --- Backend listing ---

export async function listBackends(): Promise<BackendEntry[]> {
  try {
    const res = await fetch("/api/mantle/backends");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("List backends failed:", e);
    return [];
  }
}

export async function deleteBackend(name: string): Promise<boolean> {
  try {
    const res = await fetch(`/api/mantle/backends/${encodeURIComponent(name)}`, { method: "DELETE" });
    return res.ok;
  } catch {
    return false;
  }
}

// --- Tasks ---

export async function listTasks(): Promise<MantleTask[]> {
  try {
    const res = await fetch("/api/mantle/tasks");
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch (e) {
    console.error("List tasks failed:", e);
    return [];
  }
}

export async function getTask(id: string): Promise<MantleTask | null> {
  try {
    const res = await fetch(`/api/mantle/tasks/${id}`);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return await res.json();
  } catch {
    return null;
  }
}

// --- SSE progress stream ---

export function streamDownloadProgress(taskID: string, onProgress: (data: any) => void): () => void {
  const es = new EventSource(`/api/mantle/models/download/${taskID}/stream`);
  es.onmessage = (e) => {
    try {
      onProgress(JSON.parse(e.data));
    } catch { /* skip */ }
  };
  es.onerror = () => { es.close(); };
  return () => es.close();
}

export function streamBuildProgress(taskID: string, onProgress: (data: any) => void): () => void {
  const es = new EventSource(`/api/mantle/backends/build/${taskID}/stream`);
  es.onmessage = (e) => {
    try {
      onProgress(JSON.parse(e.data));
    } catch { /* skip */ }
  };
  es.onerror = () => { es.close(); };
  return () => es.close();
}
