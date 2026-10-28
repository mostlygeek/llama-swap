import { describe, it, expect } from "vitest";
import {
  classifyModel,
  groupModelsByClass,
  MODEL_CLASS_ORDER,
  modelClassLabels,
} from "./modelClasses";

// Minimal fixtures shaped like /v1/models records. Every case mirrors a real
// model from a live llama-swap instance, so the matrix below is the regression
// net for classification changes.
function fixture(
  capabilities?: Record<string, boolean>,
  out: string[] = [],
  input: string[] = [],
) {
  const modalities = input.length || out.length ? { in: input, out } : undefined;
  return { id: "test", capabilities, modalities };
}

describe("classifyModel", () => {
  it("classifies a plain chat model (text->text, tools)", () => {
    // live: Artemis-31B
    expect(
      classifyModel(
        fixture({ function_calling: true }, ["text"], ["text"]),
      ),
    ).toBe("chat");
  });

  it("keeps vision chat models in chat (text+image->text)", () => {
    // live: Muse-Glimmer-30B, Qwen3.8-27B
    expect(
      classifyModel(
        fixture({ vision: true, function_calling: true }, ["text"], ["text", "image"]),
      ),
    ).toBe("chat");
  });

  it("keeps audio-input chat models in chat - only audio output makes Audio", () => {
    // live: LFM3-Omni-2B (audio_transcriptions, but outputs text only)
    expect(
      classifyModel(
        fixture(
          { vision: true, audio_transcriptions: true, function_calling: true },
          ["text"],
          ["text", "image", "audio"],
        ),
      ),
    ).toBe("chat");
  });

  it("classifies image generation by capability flag", () => {
    // live: image-Anima
    expect(
      classifyModel(fixture({ image_generation: true }, ["image"], ["text"])),
    ).toBe("image");
  });

  it("classifies image-to-image as image", () => {
    expect(
      classifyModel(fixture({ image_to_image: true }, ["image"], ["image"])),
    ).toBe("image");
  });

  it("classifies video by output modality, which has no capability flag", () => {
    // live: video-MiniMax-H3 (text->video, no flags)
    expect(classifyModel(fixture(undefined, ["video"], ["text"]))).toBe("video");
  });

  it("classifies audio output as audio", () => {
    // live: audio.cpp (audio+text -> audio+text)
    expect(
      classifyModel(
        fixture(
          { audio_speech: true, audio_transcriptions: true },
          ["audio", "text"],
          ["audio", "text"],
        ),
      ),
    ).toBe("audio");
  });

  it("classifies audio output without the flag by output modality", () => {
    expect(classifyModel(fixture(undefined, ["audio"], ["text"]))).toBe("audio");
  });

  it("classifies by the embeddings flag, which wins over text->text", () => {
    // live after the flag lands: embed-gte-modernbert-base probes as text->text
    expect(
      classifyModel(fixture({ embeddings: true }, ["text"], ["text"])),
    ).toBe("embed");
  });

  it("classifies a signal-less model by the reranker flag", () => {
    // live: rerank-gte-reranker-modernbert-base (only the flag is set)
    expect(classifyModel(fixture({ reranker: true }))).toBe("rerank");
  });

  it("falls back to chat for a model with no signals at all", () => {
    // live pre-flag: embed-embeddinggemma-2 (context only, nothing advertised)
    expect(classifyModel(fixture())).toBe("chat");
  });

  it("checks reranker before image", () => {
    expect(
      classifyModel(fixture({ reranker: true, image_generation: true }, ["image"])),
    ).toBe("rerank");
  });
});

describe("groupModelsByClass", () => {
  it("returns every class in MODEL_CLASS_ORDER, empty ones included", () => {
    const groups = groupModelsByClass([]);
    expect(groups.map((g) => g.class)).toEqual([...MODEL_CLASS_ORDER]);
    expect(groups.every((g) => g.models.length === 0)).toBe(true);
  });

  it("sorts each group with locale-aware numeric compare", () => {
    // ASCII order would put every uppercase ID before lowercase ones, so
    // gemma-4 lands after Qwen3.5; localeCompare orders them together.
    const groups = groupModelsByClass([
      { ...fixture({ function_calling: true }, ["text"], ["text"]), id: "Qwen3.8-27B" },
      { ...fixture({ vision: true }, ["text"], ["text", "image"]), id: "gemma-4-26B-A4B" },
      { ...fixture({}, ["text"], ["text"]), id: "Artemis-31B" },
      { ...fixture({}, ["text"], ["text"]), id: "Qwen3.5-9B" },
    ]);
    const chat = groups.find((g) => g.class === "chat");
    expect(chat?.models.map((m) => m.id)).toEqual([
      "Artemis-31B",
      "gemma-4-26B-A4B",
      "Qwen3.5-9B",
      "Qwen3.8-27B",
    ]);
  });

  it("splits a mixed fleet into separate classes", () => {
    const groups = groupModelsByClass([
      { ...fixture({ image_generation: true }, ["image"], ["text"]), id: "Anima" },
      { ...fixture(undefined, ["video"], ["text"]), id: "MiniMax-H3" },
      { ...fixture({}, ["text"], ["text"]), id: "Artemis-31B" },
    ]);
    const nonEmpty = groups.filter((g) => g.models.length > 0);
    expect(nonEmpty.map((g) => g.class)).toEqual(["chat", "image", "video"]);
  });
});

describe("class metadata", () => {
  it("labels every class", () => {
    for (const cls of MODEL_CLASS_ORDER) {
      expect(modelClassLabels[cls]).toBeTruthy();
    }
  });
});
