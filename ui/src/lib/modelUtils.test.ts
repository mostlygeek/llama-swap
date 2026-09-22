import { describe, it, expect } from "vitest";
import {
  buildModelOptions,
  filterModelOptions,
  groupModels,
  matchesCapabilities,
  matchesModel,
  modelServerPath,
  MODEL_GROUP_LOCAL,
  MODEL_GROUP_MATCHING,
  MODEL_GROUP_PEERS,
  MODEL_GROUP_PROFILE,
  MODEL_GROUP_SELECTORS,
} from "./modelUtils";
import type { Model } from "./types";

function makeModel(overrides: Partial<Model> = {}): Model {
  return {
    id: "test-model",
    state: "ready",
    name: "Test Model",
    description: "",
    unlisted: false,
    peerID: "",
    ...overrides,
  };
}

describe("modelServerPath", () => {
  it("uses the ComfyUI endpoint for the reserved model", () => {
    expect(modelServerPath("comfyui_auto")).toBe("/comfyui/");
  });

  it("uses the encoded upstream endpoint for other models", () => {
    expect(modelServerPath("org/model name")).toBe("/upstream/org%2Fmodel%20name/");
  });
});

describe("matchesCapabilities", () => {
  it("returns true when required is empty", () => {
    const model = makeModel();
    expect(matchesCapabilities(model, [])).toBe(true);
  });

  it("returns false when model has no capabilities", () => {
    const model = makeModel();
    expect(matchesCapabilities(model, ["vision"])).toBe(false);
  });

  it("returns false when model has empty capabilities object", () => {
    const model = makeModel({ capabilities: {} });
    expect(matchesCapabilities(model, ["vision"])).toBe(false);
  });

  it("returns true when model has the single required capability", () => {
    const model = makeModel({ capabilities: { vision: true } });
    expect(matchesCapabilities(model, ["vision"])).toBe(true);
  });

  it("returns false when model lacks the required capability", () => {
    const model = makeModel({ capabilities: { vision: true } });
    expect(matchesCapabilities(model, ["audio_transcriptions"])).toBe(false);
  });

  it("AND semantics: returns true only when all required are present", () => {
    const model = makeModel({ capabilities: { vision: true, audio_transcriptions: true } });
    expect(matchesCapabilities(model, ["vision", "audio_transcriptions"])).toBe(true);
    expect(matchesCapabilities(model, ["vision", "reranker"])).toBe(false);
  });

  it("matchAny=true: returns true when at least one required is present", () => {
    const model = makeModel({ capabilities: { vision: true } });
    expect(matchesCapabilities(model, ["vision", "reranker"], true)).toBe(true);
    expect(matchesCapabilities(model, ["audio_transcriptions", "reranker"], true)).toBe(false);
  });

  it("matchAny=true with empty required returns true", () => {
    const model = makeModel();
    expect(matchesCapabilities(model, [], true)).toBe(true);
  });
});

describe("matchesModel", () => {
  const chat = makeModel({ modalities: { in: ["text"], out: ["text"] } });
  const vision = makeModel({ modalities: { in: ["text", "image"], out: ["text"] } });
  const imageGen = makeModel({ modalities: { in: ["text"], out: ["image"] } });

  it("matches everything when the matcher is empty or missing", () => {
    expect(matchesModel(chat)).toBe(true);
    expect(matchesModel(chat, {})).toBe(true);
    expect(matchesModel(chat, { capabilities: [], inputModalities: [] })).toBe(true);
  });

  it("matches a model reporting any of the wanted input modalities", () => {
    expect(matchesModel(chat, { inputModalities: ["text", "image"] })).toBe(true);
    expect(matchesModel(vision, { inputModalities: ["image"] })).toBe(true);
    expect(matchesModel(chat, { inputModalities: ["image"] })).toBe(false);
  });

  it("matches a model reporting any of the wanted output modalities", () => {
    expect(matchesModel(imageGen, { outputModalities: ["image"] })).toBe(true);
    expect(matchesModel(chat, { outputModalities: ["image"] })).toBe(false);
  });

  it("requires every field that is set to hold", () => {
    const matcher = { inputModalities: ["text", "image"], outputModalities: ["text"] };
    expect(matchesModel(vision, matcher)).toBe(true);
    // Takes text in but only makes images, so it is not a chat model.
    expect(matchesModel(imageGen, matcher)).toBe(false);
  });

  it("does not match a model that reports no modalities", () => {
    expect(matchesModel(makeModel(), { inputModalities: ["text"] })).toBe(false);
    expect(matchesModel(makeModel({ modalities: { in: [], out: [] } }), { outputModalities: ["text"] })).toBe(false);
  });

  it("combines capability flags with modalities", () => {
    const tools = makeModel({ capabilities: { function_calling: true }, modalities: { in: ["text"], out: ["text"] } });
    expect(matchesModel(tools, { capabilities: ["function_calling"], outputModalities: ["text"] })).toBe(true);
    expect(matchesModel(tools, { capabilities: ["function_calling"], outputModalities: ["image"] })).toBe(false);
    expect(matchesModel(chat, { capabilities: ["function_calling"] })).toBe(false);
  });
});

describe("groupModels", () => {
  const models: Model[] = [
    makeModel({ id: "chat-model", capabilities: { vision: true }, modalities: { in: ["text", "image"], out: ["text"] } }),
    makeModel({ id: "audio-model", capabilities: { audio_transcriptions: true }, modalities: { in: ["audio"], out: ["text"] } }),
    makeModel({ id: "no-caps-model" }),
    makeModel({ id: "peer1/peer-model", peerID: "peer1" }),
    makeModel({ id: "unlisted-model", unlisted: true, capabilities: { vision: true } }),
  ];

  it("filters out unlisted models", () => {
    const result = groupModels(models);
    expect(result.localMatching.length + result.local.length).toBe(3);
    expect([...result.localMatching, ...result.local].every((m) => !m.unlisted)).toBe(true);
  });

  it("separates peer models into peers", () => {
    const result = groupModels(models);
    expect(result.peers).toHaveLength(1);
    expect(result.peers[0].id).toBe("peer1/peer-model");
  });

  it("without a matcher, all local models go to local (non-matching)", () => {
    const result = groupModels(models);
    expect(result.localMatching).toHaveLength(0);
    expect(result.local).toHaveLength(3);
  });

  it("with capabilities, matching models go to localMatching", () => {
    const result = groupModels(models, { capabilities: ["vision"] });
    expect(result.localMatching).toHaveLength(1);
    expect(result.localMatching[0].id).toBe("chat-model");
    expect(result.local).toHaveLength(2);
  });

  it("with capabilities, models without capabilities go to local", () => {
    const result = groupModels(models, { capabilities: ["vision"] });
    expect(result.local.find((m) => m.id === "no-caps-model")).toBeDefined();
  });

  it("with matchAny, matches models with any listed capability", () => {
    const result = groupModels(models, { capabilities: ["vision", "audio_transcriptions"], matchAny: true });
    expect(result.localMatching).toHaveLength(2);
    expect(result.localMatching.map((m) => m.id)).toContain("chat-model");
    expect(result.localMatching.map((m) => m.id)).toContain("audio-model");
    expect(result.local).toHaveLength(1);
  });

  it("with modalities, models taking text or images in go to localMatching", () => {
    const result = groupModels(models, { inputModalities: ["text", "image"], outputModalities: ["text"] });
    expect(result.localMatching.map((m) => m.id)).toEqual(["chat-model"]);
    expect(result.local.map((m) => m.id)).toEqual(["audio-model", "no-caps-model"]);
  });

  it("with an empty matcher, all local go to local (non-matching)", () => {
    const result = groupModels(models, { capabilities: [] });
    expect(result.localMatching).toHaveLength(0);
    expect(result.local).toHaveLength(3);
  });
});

describe("buildModelOptions", () => {
  const models: Model[] = [
    makeModel({ id: "vision-model", name: "Vision", modalities: { in: ["text", "image"], out: ["text"] }, aliases: ["gpt-4o"] }),
    makeModel({ id: "draw-model", name: "draw-model", modalities: { in: ["text"], out: ["image"] } }),
    makeModel({ id: "peer1/remote", name: "", peerID: "peer1" }),
  ];

  it("lists matching models first, then profile, selectors, local and peers", () => {
    const options = buildModelOptions({
      models,
      profiles: [makeModel({ id: "pinned", name: "" })],
      selectors: [makeModel({ id: "auto", name: "" })],
      matcher: { inputModalities: ["text", "image"], outputModalities: ["text"] },
    });
    expect(options.map((o) => [o.group, o.value])).toEqual([
      [MODEL_GROUP_MATCHING, "vision-model"],
      [MODEL_GROUP_MATCHING, "gpt-4o"],
      [MODEL_GROUP_PROFILE, "pinned"],
      [MODEL_GROUP_SELECTORS, "auto"],
      [MODEL_GROUP_LOCAL, "draw-model"],
      [MODEL_GROUP_PEERS, "peer1/remote"],
    ]);
  });

  it("marks aliases with the model they point at", () => {
    const options = buildModelOptions({ models });
    expect(options.find((o) => o.value === "gpt-4o")?.aliasOf).toBe("vision-model");
    expect(options.find((o) => o.value === "vision-model")?.aliasOf).toBeUndefined();
  });

  it("keeps a display name only when it differs from the model ID", () => {
    const options = buildModelOptions({ models });
    expect(options.find((o) => o.value === "vision-model")?.name).toBe("Vision");
    expect(options.find((o) => o.value === "draw-model")?.name).toBeUndefined();
  });

  it("puts every model under Local when the tab asks for nothing", () => {
    const options = buildModelOptions({ models });
    expect(options.filter((o) => o.group === MODEL_GROUP_MATCHING)).toHaveLength(0);
    expect(options.filter((o) => o.group === MODEL_GROUP_LOCAL).map((o) => o.value))
      .toEqual(["vision-model", "gpt-4o", "draw-model"]);
  });
});

describe("filterModelOptions", () => {
  const options = buildModelOptions({
    models: [
      makeModel({ id: "Qwen3-30B-Instruct", name: "Qwen 3", aliases: ["fast"] }),
      makeModel({ id: "llama-3.1-8b", name: "" }),
    ],
  });

  it("returns everything for an empty or whitespace query", () => {
    expect(filterModelOptions(options, "")).toHaveLength(3);
    expect(filterModelOptions(options, "   ")).toHaveLength(3);
  });

  it("matches case insensitively on part of the model ID", () => {
    expect(filterModelOptions(options, "qwen").map((o) => o.value)).toEqual(["Qwen3-30B-Instruct", "fast"]);
    expect(filterModelOptions(options, "LLAMA").map((o) => o.value)).toEqual(["llama-3.1-8b"]);
  });

  it("requires every term to match, in any order", () => {
    // The alias comes along because it is matched on the model it points at.
    expect(filterModelOptions(options, "30b qwen").map((o) => o.value)).toEqual(["Qwen3-30B-Instruct", "fast"]);
    expect(filterModelOptions(options, "qwen 70b")).toHaveLength(0);
  });

  it("matches the display name as well as the ID", () => {
    expect(filterModelOptions(options, "qwen 3").map((o) => o.value)).toContain("Qwen3-30B-Instruct");
  });

  it("matches an alias by its own name", () => {
    expect(filterModelOptions(options, "fast").map((o) => o.value)).toEqual(["fast"]);
  });
});
