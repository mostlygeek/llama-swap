import { writable, get } from "svelte/store";
import type { MantleTask } from "../lib/types";
import { streamDownloadProgress, streamBuildProgress, listTasks } from "../lib/mantleApi";

type TaskEntry = { task: MantleTask; cleanup: () => void };

export const activeDownloads = writable<Map<string, TaskEntry>>(new Map());
export const activeBuilds = writable<Map<string, TaskEntry>>(new Map());

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

export function trackDownload(task: MantleTask) {
  const cleanup = streamDownloadProgress(task.id, (data) =>
    updateTask(activeDownloads, task.id, data)
  );
  activeDownloads.update((map) => new Map(map.set(task.id, { task, cleanup })));
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

// Called on component mount — reconnect running tasks from server
export async function syncTasks() {
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
}
