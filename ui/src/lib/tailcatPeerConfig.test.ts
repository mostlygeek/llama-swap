import { describe, it, expect } from "vitest";
import { peerConfigYaml, yamlQuote } from "./tailcatPeerConfig";

describe("yamlQuote", () => {
  it("wraps a plain identifier in quotes", () => {
    expect(yamlQuote("chat")).toBe('"chat"');
  });

  it("keeps a colon-bearing value a single quoted scalar", () => {
    expect(yamlQuote("foo: bar")).toBe('"foo: bar"');
  });

  it("escapes a leading YAML comment marker", () => {
    expect(yamlQuote("#foo")).toBe('"#foo"');
  });

  it("escapes embedded double quotes and backslashes", () => {
    expect(yamlQuote('say "hi"\\now')).toBe(JSON.stringify('say "hi"\\now'));
  });
});

describe("peerConfigYaml", () => {
  it("quotes every model identifier", () => {
    const yaml = peerConfigYaml("tcTOKEN", ["chat", "foo: bar", "#foo"]);
    expect(yaml).toContain('      - "chat"');
    expect(yaml).toContain('      - "foo: bar"');
    expect(yaml).toContain('      - "#foo"');
  });

  it("parses back to the original model list via YAML-compatible JSON escaping", () => {
    const models = ["gpu/chat", "foo: bar", "#foo", "with \"quotes\" and \\backslash"];
    const yaml = peerConfigYaml("tcTOKEN", models);
    const modelsBlock = yaml.split("models:\n")[1];
    const parsed = modelsBlock
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line.replace(/^\s*-\s*/, "")));
    expect(parsed).toEqual(models);
  });

  it("embeds the connection token in the proxy line", () => {
    const yaml = peerConfigYaml("tcTOKEN", ["chat"]);
    expect(yaml).toContain("proxy: tailcat://tcTOKEN");
  });
});
