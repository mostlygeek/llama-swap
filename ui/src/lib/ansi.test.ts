import { describe, it, expect } from "vitest";
import { ansiToHtml } from "./ansi";

describe("ansiToHtml", () => {
  it("returns escaped plain text when no ANSI codes are present", () => {
    expect(ansiToHtml("hello <world> & \"friends\"")).toBe("hello &lt;world&gt; &amp; &quot;friends&quot;");
  });

  it("returns empty string for empty input", () => {
    expect(ansiToHtml("")).toBe("");
  });

  it("renders foreground color", () => {
    expect(ansiToHtml("\x1b[32mgreen\x1b[0m")).toBe('<span style="color:#008700">green</span>');
  });

  it("renders bold combined with color", () => {
    expect(ansiToHtml("\x1b[1;31merror\x1b[0m")).toBe('<span style="font-weight:600;color:#cd0000">error</span>');
  });

  it("merges consecutive chunks with the same style", () => {
    expect(ansiToHtml("\x1b[32ma\x1b[32mb")).toBe('<span style="color:#008700">ab</span>');
  });

  it("renders plain text after reset", () => {
    expect(ansiToHtml("\x1b[32mgreen\x1b[0mplain")).toBe('<span style="color:#008700">green</span>plain');
  });

  it("renders background color", () => {
    expect(ansiToHtml("\x1b[41mred bg\x1b[0m")).toBe('<span style="background-color:#cd0000">red bg</span>');
  });

  it("renders bright colors", () => {
    expect(ansiToHtml("\x1b[93mbright yellow\x1b[0m")).toBe('<span style="color:#cdcd00">bright yellow</span>');
  });

  it("renders 256-color foreground", () => {
    expect(ansiToHtml("\x1b[38;5;196mred\x1b[0m")).toBe('<span style="color:rgb(255, 0, 0)">red</span>');
  });

  it("renders 256-color background from the base palette", () => {
    expect(ansiToHtml("\x1b[48;5;4mblue\x1b[0m")).toBe('<span style="background-color:#0000ee">blue</span>');
  });

  it("renders truecolor foreground", () => {
    expect(ansiToHtml("\x1b[38;2;10;20;30mcustom\x1b[0m")).toBe('<span style="color:rgb(10, 20, 30)">custom</span>');
  });

  it("renders italic and underline", () => {
    expect(ansiToHtml("\x1b[3;4mstyled\x1b[0m")).toBe('<span style="font-style:italic;text-decoration:underline">styled</span>');
  });

  it("treats empty SGR as reset", () => {
    expect(ansiToHtml("\x1b[32ma\x1b[mplain")).toBe('<span style="color:#008700">a</span>plain');
  });

  it("drops non-SGR CSI sequences", () => {
    expect(ansiToHtml("\x1b[2Kline")).toBe("line");
  });

  it("closes open span at end of input without reset", () => {
    expect(ansiToHtml("\x1b[32mgreen")).toBe('<span style="color:#008700">green</span>');
  });

  it("escapes HTML inside colored segments", () => {
    expect(ansiToHtml("\x1b[32m<a>\x1b[0m")).toBe('<span style="color:#008700">&lt;a&gt;</span>');
  });

  it("handles a typical llama-server colored log line", () => {
    const line = "\x1b[1;32m[server]\x1b[0m loading model";
    expect(ansiToHtml(line)).toBe('<span style="font-weight:600;color:#008700">[server]</span> loading model');
  });

  describe("dark palette", () => {
    it("renders brighter foreground colors", () => {
      expect(ansiToHtml("\x1b[32mgreen\x1b[0m", true)).toBe('<span style="color:#50fa7b">green</span>');
    });

    it("renders brighter blue for timestamps", () => {
      expect(ansiToHtml("\x1b[34mblue\x1b[0m", true)).toBe('<span style="color:#61afef">blue</span>');
    });

    it("renders brighter bright colors", () => {
      expect(ansiToHtml("\x1b[93mbright yellow\x1b[0m", true)).toBe('<span style="color:#f5fc9e">bright yellow</span>');
    });

    it("renders 256-color base palette from the dark palette", () => {
      expect(ansiToHtml("\x1b[48;5;4mblue\x1b[0m", true)).toBe('<span style="background-color:#61afef">blue</span>');
    });

    it("renders 256-color cube colors identically in both palettes", () => {
      expect(ansiToHtml("\x1b[38;5;196mred\x1b[0m", true)).toBe('<span style="color:rgb(255, 0, 0)">red</span>');
    });
  });
});
