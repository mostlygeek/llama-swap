import type { Model } from "./types";

export interface GroupedModels {
  local: Model[];
  peersByProvider: Record<string, Model[]>;
}

export function groupModels(models: Model[]): GroupedModels {
  const available = models.filter((m) => !m.unlisted);
  const local = available.filter((m) => !m.peerID);
  const peerModels = available.filter((m) => m.peerID);

  const peersByProvider = peerModels.reduce(
    (acc, model) => {
      const peerId = model.peerID || "unknown";
      if (!acc[peerId]) acc[peerId] = [];
      acc[peerId].push(model);
      return acc;
    },
    {} as Record<string, Model[]>
  );

  return { local, peersByProvider };
}
