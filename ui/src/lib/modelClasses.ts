import type { Model } from "./types";

/**
 * The class a model belongs to on the Models list, derived from /v1/models
 * capability flags and modality lists - never from the model ID, so IDs stay
 * free of grouping prefixes.
 */
export type ModelClass = "chat" | "image" | "video" | "audio" | "embed" | "rerank";

/** Section order for the Models list: chat first as the most-used class, then the generation modalities, then the retrieval helpers. */
export const MODEL_CLASS_ORDER: readonly ModelClass[] = [
  "chat",
  "image",
  "video",
  "audio",
  "embed",
  "rerank",
];

export const modelClassLabels: Record<ModelClass, string> = {
  chat: "Chat",
  image: "Image",
  video: "Video",
  audio: "Audio",
  embed: "Embeddings",
  rerank: "Rerank",
};

type Classifiable = Pick<Model, "capabilities" | "modalities">;

/**
 * Maps one model onto its class. Checks run most-specific first because
 * classes overlap in their modality lists:
 * - a speech model also outputs text, so output checks precede the chat fallback;
 * - a chat model may accept audio input (audio_transcriptions) without
 *   producing audio - only audio OUTPUT makes a model Audio, so LFM3-Omni-style
 *   omni chat models stay in Chat;
 * - an embedding model can surface as text->text from capability discovery, so
 *   the explicit embeddings flag must win when set.
 */
export function classifyModel(model: Classifiable): ModelClass {
  // Record cast, matching matchesCapabilities: the embeddings key arrives
  // from /v1/models at runtime but is not declared on ModelCapabilities here.
  const caps = (model.capabilities ?? {}) as Record<string, boolean>;
  if (caps.reranker) return "rerank";
  // A config can declare out: [image] with no input modalities, which derives
  // no image_generation flag, so the output modality is checked too.
  if (caps.image_generation || caps.image_to_image) return "image";
  if (model.modalities?.out?.includes("image")) return "image";
  if (model.modalities?.out?.includes("video")) return "video";
  if (caps.audio_speech || model.modalities?.out?.includes("audio")) return "audio";
  if (caps.embeddings) return "embed";
  return "chat";
}

export interface ModelClassGroup<T extends { id: string } & Classifiable> {
  class: ModelClass;
  models: T[];
}

/**
 * Aggregate of the row status dots: green when any model in the group is
 * ready, amber while one is starting or stopping, gray otherwise. Mirrors the
 * per-model status dot so a collapsed section or folded sub-menu still shows
 * whether that group has something loaded.
 */
export function sectionStatusDotColor(models: Pick<Model, "state">[]): string {
  let transitioning = false;
  for (const model of models) {
    if (model.state === "ready") return "bg-success";
    if (model.state === "starting" || model.state === "stopping") transitioning = true;
  }
  return transitioning ? "bg-warning" : "bg-muted-foreground/40";
}

/**
 * Groups models by class in MODEL_CLASS_ORDER, sorting each group by ID with a
 * locale-aware numeric compare - the backend sorts /v1/models by raw ASCII,
 * which scatters mixed-case IDs (gemma-4 lands after every uppercase name)
 * and cannot order Qwen3.5 against Qwen3.8 numerically.
 */
export function groupModelsByClass<T extends { id: string } & Classifiable>(
  models: T[],
): ModelClassGroup<T>[] {
  return MODEL_CLASS_ORDER.map((cls) => ({
    class: cls,
    models: models
      .filter((m) => classifyModel(m) === cls)
      .sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true })),
  }));
}
