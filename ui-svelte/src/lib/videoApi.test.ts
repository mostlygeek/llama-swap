import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createVideoJob,
  createVideoSync,
  deleteVideo,
  generateVideoAsync,
  getVideoContent,
  getVideoStatus,
} from "./videoApi";
import type { VideoJob } from "./types";

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => body, text: async () => JSON.stringify(body) };
}

function blobResponse(blob: Blob, ok = true, status = 200) {
  return { ok, status, blob: async () => blob, text: async () => "error" };
}

describe("createVideoJob", () => {
  it("posts a multipart form to /v1/videos and returns the job", async () => {
    const job: VideoJob = { id: "video-1", status: "queued", created_at: 1 };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(job));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      createVideoJob("my-model", "a cat riding a skateboard", { size: "1280x720", seconds: 5, fps: 24 })
    ).resolves.toEqual(job);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/v1/videos");
    expect(init.method).toBe("POST");
    const body = init.body as FormData;
    expect(body.get("model")).toBe("my-model");
    expect(body.get("prompt")).toBe("a cat riding a skateboard");
    expect(body.get("size")).toBe("1280x720");
    expect(body.get("seconds")).toBe("5");
    expect(body.get("fps")).toBe("24");
    expect(body.get("input_reference")).toBeNull();
  });

  it("includes the reference file when provided", async () => {
    const job: VideoJob = { id: "video-1", status: "queued" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(job));
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["data"], "ref.png", { type: "image/png" });

    await createVideoJob("my-model", "animate this", {}, file);

    const body = fetchMock.mock.calls[0][1].body as FormData;
    expect(body.get("input_reference")).toBe(file);
  });

  it("sends negativePrompt and flattens advanced fields, dropping an advanced model override", async () => {
    const job: VideoJob = { id: "video-1", status: "queued" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(job));
    vi.stubGlobal("fetch", fetchMock);

    await createVideoJob("my-model", "a bear playing with yarn", {
      negativePrompt: "low quality, blurry, static",
      advanced: {
        width: 832,
        height: 480,
        seed: 42,
        extra_params: { sample_solver: "euler" },
        model: "should-not-override",
      },
    });

    const body = fetchMock.mock.calls[0][1].body as FormData;
    expect(body.get("model")).toBe("my-model");
    expect(body.get("negative_prompt")).toBe("low quality, blurry, static");
    expect(body.get("width")).toBe("832");
    expect(body.get("height")).toBe("480");
    expect(body.get("seed")).toBe("42");
    expect(body.get("extra_params")).toBe('{"sample_solver":"euler"}');
  });

  it("lets advanced fields override same-named basic params", async () => {
    const job: VideoJob = { id: "video-1", status: "queued" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(job));
    vi.stubGlobal("fetch", fetchMock);

    await createVideoJob("my-model", "prompt", { fps: 24, advanced: { fps: 30 } });

    const body = fetchMock.mock.calls[0][1].body as FormData;
    expect(body.get("fps")).toBe("30");
  });

  it("throws with the status and body on a non-ok response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: false, status: 400, text: async () => "bad request" }));

    await expect(createVideoJob("my-model", "prompt", {})).rejects.toThrow("Video API error: 400 - bad request");
  });
});

describe("createVideoSync", () => {
  it("posts to /v1/videos/sync and returns the raw video bytes", async () => {
    const blob = new Blob(["bytes"], { type: "video/mp4" });
    const fetchMock = vi.fn().mockResolvedValue(blobResponse(blob));
    vi.stubGlobal("fetch", fetchMock);

    await expect(createVideoSync("my-model", "prompt", { size: "1280x720" })).resolves.toBe(blob);
    expect(fetchMock.mock.calls[0][0]).toBe("/v1/videos/sync");
  });
});

describe("getVideoStatus / getVideoContent / deleteVideo", () => {
  it("dispatches by a ?model= query param since the job id alone doesn't identify a backend", async () => {
    const job: VideoJob = { id: "video-1", status: "completed" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(job));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getVideoStatus("video-1", "my model")).resolves.toEqual(job);
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/videos/video-1?model=my%20model",
      expect.objectContaining({ cache: "no-store" })
    );
  });

  it("getVideoContent fetches the content endpoint and returns a blob", async () => {
    const blob = new Blob(["bytes"]);
    const fetchMock = vi.fn().mockResolvedValue(blobResponse(blob));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getVideoContent("video-1", "my-model")).resolves.toBe(blob);
    expect(fetchMock).toHaveBeenCalledWith("/v1/videos/video-1/content?model=my-model", expect.anything());
  });

  it("deleteVideo issues a DELETE request", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => "" });
    vi.stubGlobal("fetch", fetchMock);

    await deleteVideo("video-1", "my-model");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/v1/videos/video-1?model=my-model");
    expect(init.method).toBe("DELETE");
  });
});

describe("generateVideoAsync", () => {
  it("polls until completed and returns the rendered video", async () => {
    const fetchMock = vi
      .fn()
      // createVideoJob
      .mockResolvedValueOnce(jsonResponse({ id: "video-1", status: "queued" }))
      // getVideoStatus (still running)
      .mockResolvedValueOnce(jsonResponse({ id: "video-1", status: "in_progress" }))
      // getVideoStatus (done)
      .mockResolvedValueOnce(jsonResponse({ id: "video-1", status: "completed" }))
      // getVideoContent
      .mockResolvedValueOnce(blobResponse(new Blob(["bytes"])));
    vi.stubGlobal("fetch", fetchMock);

    const statuses: string[] = [];
    const controller = new AbortController();
    const blob = await generateVideoAsync(
      "my-model",
      "prompt",
      {},
      undefined,
      controller.signal,
      (job) => statuses.push(job.status),
      { pollIntervalMs: 0 }
    );

    expect(await blob.text()).toBe("bytes");
    expect(statuses).toEqual(["queued", "in_progress", "completed"]);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("throws when the job reaches a terminal failure status", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ id: "video-1", status: "queued" }))
      .mockResolvedValueOnce(jsonResponse({ id: "video-1", status: "failed", error: "out of memory" }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      generateVideoAsync("my-model", "prompt", {}, undefined, new AbortController().signal, undefined, {
        pollIntervalMs: 0,
      })
    ).rejects.toThrow("Video generation failed: out of memory");
  });

  it("times out when the job never reaches a terminal status", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ id: "video-1", status: "queued" }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      generateVideoAsync("my-model", "prompt", {}, undefined, new AbortController().signal, undefined, {
        pollIntervalMs: 0,
        timeoutMs: -1,
      })
    ).rejects.toThrow("Timed out waiting for video generation to complete");
  });
});
