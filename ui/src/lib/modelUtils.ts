import type { Model } from "./types";

export interface GroupedModels {
  local: Model[];
  localMatching: Model[];
  peers: Model[];
}

export function modelServerPath(modelId: string): string {
  if (modelId === "comfyui_auto") return "/comfyui/";
  return `/upstream/${encodeURIComponent(modelId)}/`;
}

export function matchesCapabilities(model: Model, required: string[], matchAny = false): boolean {
  if (!required.length) return true;
  if (!model.capabilities) return false;
  const caps = model.capabilities as Record<string, boolean>;
  if (matchAny) {
    return required.some((cap) => caps[cap] === true);
  }
  return required.every((cap) => caps[cap] === true);
}

export function groupModels(models: Model[], capabilities?: string[], matchAny = false): GroupedModels {
  const available = models.filter((m) => !m.unlisted);
  const local = available.filter((m) => !m.peerID);
  const peers = available.filter((m) => m.peerID);

  let localMatching: Model[] = [];
  let localRest: Model[] = [];

  if (capabilities && capabilities.length > 0) {
    for (const model of local) {
      if (matchesCapabilities(model, capabilities, matchAny)) {
        localMatching.push(model);
      } else {
        localRest.push(model);
      }
    }
  } else {
    localRest = local;
  }

  return { local: localRest, localMatching, peers };
}
