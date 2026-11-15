import { describe, expect, it, vi } from "vitest";
import { appendImageFiles, pasteImageFiles } from "./chatImages";

const png = (name: string) => new File(["image"], name, { type: "image/png" });

describe("pasteImageFiles", () => {
  it("attaches only image files and handles the paste", () => {
    const image = png("screenshot.png");
    const text = new File(["hello"], "notes.txt", { type: "text/plain" });
    const preventDefault = vi.fn();
    const attach = vi.fn();

    pasteImageFiles({ clipboardData: { files: [text, image] }, preventDefault }, attach);

    expect(attach).toHaveBeenCalledExactlyOnceWith([image]);
    expect(preventDefault).toHaveBeenCalledOnce();
  });

  it("leaves ordinary text paste to the browser", () => {
    const preventDefault = vi.fn();
    const attach = vi.fn();

    pasteImageFiles({ clipboardData: { files: [] }, preventDefault }, attach);

    expect(attach).not.toHaveBeenCalled();
    expect(preventDefault).not.toHaveBeenCalled();
  });
});

describe("appendImageFiles", () => {
  it("discards image reads that finish after the composer changes chats", async () => {
    const pending = Promise.withResolvers<string>();
    const replaceImages = vi.fn();
    let current = true;
    const result = appendImageFiles([png("private.png")], () => [], replaceImages,
      () => pending.promise, () => current);
    current = false;
    pending.resolve("data:image/png;base64,private");
    await expect(result).resolves.toBeNull();
    expect(replaceImages).not.toHaveBeenCalled();
  });

  it("does not surface stale image-read errors in another chat", async () => {
    const pending = Promise.withResolvers<string>();
    let current = true;
    const result = appendImageFiles([png("private.png")], () => [], vi.fn(),
      () => pending.promise, () => current);
    current = false;
    pending.reject(new Error("Read failed"));
    await expect(result).resolves.toBeNull();
  });
  it("rejects an unsupported image before reading the file", async () => {
    const svg = new File(["<svg/>"], "image.svg", { type: "image/svg+xml" });
    const readImage = vi.fn();
    const replaceImages = vi.fn();

    await expect(appendImageFiles([svg], () => [], replaceImages, readImage)).resolves.toMatch(/Invalid file type/);
    expect(readImage).not.toHaveBeenCalled();
    expect(replaceImages).not.toHaveBeenCalled();
  });

  it("rejects an oversized image before reading the file", async () => {
    const large = new File([new Uint8Array(20 * 1024 * 1024 + 1)], "large.png", { type: "image/png" });
    const readImage = vi.fn();
    const replaceImages = vi.fn();

    await expect(appendImageFiles([large], () => [], replaceImages, readImage)).resolves.toMatch(/Maximum size: 20MB/);
    expect(readImage).not.toHaveBeenCalled();
    expect(replaceImages).not.toHaveBeenCalled();
  });

  it("keeps the five-image limit when reads complete out of order", async () => {
    let images = ["existing-1", "existing-2", "existing-3"];
    const currentImages = () => images;
    const replaceImages = (next: string[]) => { images = next; };
    const slow = Promise.withResolvers<string>();
    const first = appendImageFiles([png("slow.png")], currentImages, replaceImages, () => slow.promise);
    const second = appendImageFiles(
      [png("fast-1.png"), png("fast-2.png")],
      currentImages,
      replaceImages,
      async (file) => file.name,
    );

    await expect(second).resolves.toBeNull();
    slow.resolve("slow.png");
    await expect(first).resolves.toMatch(/Maximum 5 images/);
    expect(images).toEqual(["existing-1", "existing-2", "existing-3", "fast-1.png", "fast-2.png"]);
  });
});
