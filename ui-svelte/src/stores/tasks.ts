import { writable, get } from "svelte/store";
import type { MantleTask } from "../lib/types";
import { streamDownloadProgress, streamBuildProgress, listTasks, startDownload } from "../lib/mantleApi";

type DownloadEntry = { task: MantleTask; cleanup: () => void; filename?: string };
type BuildEntry    = { task: MantleTask; cleanup: () => void };

export const activeDownloads = writable<Map<string, DownloadEntry>>(new Map());
export const activeBuilds    = writable<Map<string, BuildEntry>>(new Map());

function updateTask(
  store: typeof activeDownloads | typeof activeBuilds,
  taskID: string,
  data: Partial<MantleTask>
) {
  store.update((map) => {
    const entry = map.get(taskID);
    if (entry) {
      entry.task = { ...entry.task, ...data };
      return new Map(map);
    }
    return map;
  });
}

export function trackDownload(task: MantleTask, filename?: string) {
  const cleanup = streamDownloadProgress(task.id, (data) =>
    updateTask(activeDownloads, task.id, data)
  );
  activeDownloads.update((map) => new Map(map.set(task.id, { task, cleanup, filename })));
}

export function trackBuild(task: MantleTask) {
  const cleanup = streamBuildProgress(task.id, (data) =>
    updateTask(activeBuilds, task.id, data)
  );
  activeBuilds.update((map) => new Map(map.set(task.id, { task, cleanup })));
}

export function removeDownload(taskID: string) {
  activeDownloads.update((map) => {
    map.get(taskID)?.cleanup();
    map.delete(taskID);
    return new Map(map);
  });
}

export function removeBuild(taskID: string) {
  activeBuilds.update((map) => {
    map.get(taskID)?.cleanup();
    map.delete(taskID);
    return new Map(map);
  });
}

export async function retryDownload(taskID: string) {
  const entry = get(activeDownloads).get(taskID);
  if (!entry?.filename || !entry.task.modelID) return;
  const { filename, task: { modelID } } = entry;
  removeDownload(taskID);
  const newTask = await startDownload(modelID, filename);
  if (newTask) trackDownload(newTask, filename);
}

let syncing = false;
export async function syncTasks() {
  if (syncing) return;
  syncing = true;
  try {
    const tasks = await listTasks();
    const downloads = get(activeDownloads);
    const builds = get(activeBuilds);
    for (const task of tasks) {
      if (task.type === "download") {
        if (task.state === "running" && !downloads.has(task.id)) {
          trackDownload(task);
        } else {
          updateTask(activeDownloads, task.id, task);
        }
      } else if (task.type === "build") {
        if (task.state === "running" && !builds.has(task.id)) {
          trackBuild(task);
        } else {
          updateTask(activeBuilds, task.id, task);
        }
      }
    }
  } finally {
    syncing = false;
  }
}