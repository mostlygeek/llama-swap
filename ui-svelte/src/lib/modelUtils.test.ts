import { describe, it, expect } from "vitest";
import { matchesCapabilities, groupModels } from "./modelUtils";
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

describe("groupModels", () => {
  const models: Model[] = [
    makeModel({ id: "chat-model", capabilities: { vision: true } }),
    makeModel({ id: "audio-model", capabilities: { audio_transcriptions: true } }),
    makeModel({ id: "no-caps-model" }),
    makeModel({ id: "peer-model", peerID: "peer1" }),
    makeModel({ id: "unlisted-model", unlisted: true, capabilities: { vision: true } }),
  ];

  it("filters out unlisted models", () => {
    const result = groupModels(models);
    expect(result.localMatching.length + result.local.length).toBe(3);
    expect([...result.localMatching, ...result.local].every((m) => !m.unlisted)).toBe(true);
  });

  it("separates peer models into peersByProvider", () => {
    const result = groupModels(models);
    expect(result.peersByProvider["peer1"]).toHaveLength(1);
    expect(result.peersByProvider["peer1"][0].id).toBe("peer-model");
  });

  it("without capabilities, all local models go to local (non-matching)", () => {
    const result = groupModels(models);
    expect(result.localMatching).toHaveLength(0);
    expect(result.local).toHaveLength(3);
  });

  it("with capabilities, matching models go to localMatching", () => {
    const result = groupModels(models, ["vision"]);
    expect(result.localMatching).toHaveLength(1);
    expect(result.localMatching[0].id).toBe("chat-model");
    expect(result.local).toHaveLength(2);
  });

  it("with capabilities, models without capabilities go to local", () => {
    const result = groupModels(models, ["vision"]);
    expect(result.local.find((m) => m.id === "no-caps-model")).toBeDefined();
  });

  it("with matchAny, matches models with any listed capability", () => {
    const result = groupModels(models, ["vision", "audio_transcriptions"], true);
    expect(result.localMatching).toHaveLength(2);
    expect(result.localMatching.map((m) => m.id)).toContain("chat-model");
    expect(result.localMatching.map((m) => m.id)).toContain("audio-model");
    expect(result.local).toHaveLength(1);
  });

  it("with empty capabilities array, all local go to local (non-matching)", () => {
    const result = groupModels(models, []);
    expect(result.localMatching).toHaveLength(0);
    expect(result.local).toHaveLength(3);
  });
});
