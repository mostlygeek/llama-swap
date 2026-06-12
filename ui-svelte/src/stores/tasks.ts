import { writable, get } from "svelte/store";
import type { MantleTask } from "../lib/types";
import { streamDownloadProgress, streamBuildProgress, listTasks, startDownload } from "../lib/mantleApi";

type DownloadEntry = { task: MantleTask; cleanup: () => void; filename?: string };
type BuildEntry    = { task: MantleTask; cleanup: () => void };
type ProgressData = Partial<MantleTask> & { taskID?: string };

export const activeDownloads = writable<Map<string, DownloadEntry>>(new Map());
export const activeBuilds    = writable<Map<string, BuildEntry>>(new Map());

function updateTask(
  store: typeof activeDownloads | typeof activeBuilds,
  taskID: string,
  data: ProgressData
) {
  store.update((map) => {
    const id = data.id ?? data.taskID ?? taskID;
    const entry = map.get(id);
    if (entry) {
      const { taskID: _taskID, ...taskData } = data;
      entry.task = { ...entry.task, ...taskData, id };
      return new Map(map);
    }
    return map;
  });
}

export function trackDownload(task: MantleTask, filename?: string) {
  const existing = get(activeDownloads).get(task.id);
  if (existing) {
    updateTask(activeDownloads, task.id, task);
    return;
  }
  const cleanup = streamDownloadProgress(task.id, (data) =>
    updateTask(activeDownloads, task.id, data)
  );
  activeDownloads.update((map) => new Map(map.set(task.id, { task, cleanup, filename })));
}

export function trackBuild(task: MantleTask) {
  const existing = get(activeBuilds).get(task.id);
  if (existing) {
    updateTask(activeBuilds, task.id, task);
    return;
  }
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
    for (const task of tasks) {
      if (task.type === "download") {
        if (task.state === "running") {
          trackDownload(task);
        } else {
          updateTask(activeDownloads, task.id, task);
        }
      } else if (task.type === "build") {
        if (task.state === "running") {
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
