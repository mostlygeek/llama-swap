import { describe, expect, it } from "vitest";
import { loadingMessage, loadingPercent } from "./modelLoad";
import type { Model } from "../lib/types";

function model(fields: Partial<Model>): Model {
  return { id: "m", state: "stopped", name: "", description: "", unlisted: false, peerID: "", ...fields };
}

describe("loadingPercent", () => {
  it("rounds the reported fraction to a whole percentage", () => {
    expect(loadingPercent(model({ state: "starting", loadingProgress: 0.426 }))).toBe(43);
    expect(loadingPercent(model({ state: "starting", loadingProgress: 0 }))).toBe(0);
  });

  it("is undefined without progress or once the model is no longer starting", () => {
    expect(loadingPercent(model({ state: "starting" }))).toBeUndefined();
    expect(loadingPercent(model({ state: "ready", loadingProgress: 1 }))).toBeUndefined();
    expect(loadingPercent(undefined)).toBeUndefined();
  });
});

describe("loadingMessage", () => {
  it("is only shown while starting", () => {
    expect(loadingMessage(model({ state: "starting", loadingMessage: "loading tensors" }))).toBe("loading tensors");
    expect(loadingMessage(model({ state: "ready", loadingMessage: "loading tensors" }))).toBe("");
    expect(loadingMessage(model({ state: "starting" }))).toBe("");
  });
});
