import { get } from "svelte/store";
import { beforeEach, describe, expect, it } from "vitest";
import { currentServerKey, perServerKey } from "./serverKey";
import { activeServerID, tailcatServers, upsert } from "../tailcat/servers";

beforeEach(() => {
  tailcatServers.set([]);
  activeServerID.set("");
});

// No DOM in this suite, so "no active Tailcat server" falls back to the
// "default" branch rather than an actual window.location.host; that branch is
// exercised by the browser these modules actually run in.
describe("currentServerKey", () => {
  it("falls back when no Tailcat server is active", () => {
    expect(currentServerKey()).toBe("default");
  });

  it("identifies the active Tailcat server instead of falling back", () => {
    tailcatServers.set(upsert(get(tailcatServers), { id: "a", name: "node-a", token: "tcA", apiKey: "", derpMapURL: "" }));
    activeServerID.set("a");
    expect(currentServerKey()).toBe("tailcat:a");
  });

  it("falls back when the active id no longer names a saved server", () => {
    activeServerID.set("gone");
    expect(currentServerKey()).toBe("default");
  });
});

describe("perServerKey", () => {
  it("gives different servers distinct keys for the same storage key", () => {
    tailcatServers.set(
      upsert(
        upsert(get(tailcatServers), { id: "a", name: "node-a", token: "tcA", apiKey: "", derpMapURL: "" }),
        { id: "b", name: "node-b", token: "tcB", apiKey: "", derpMapURL: "" },
      ),
    );

    activeServerID.set("a");
    const keyA = perServerKey("playground-selected-model");
    activeServerID.set("b");
    const keyB = perServerKey("playground-selected-model");

    expect(keyA).not.toBe(keyB);
  });
});
