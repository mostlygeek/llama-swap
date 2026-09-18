import { describe, expect, it } from "vitest";

import type { Job } from "./job";
import { JobQueue } from "./queue";

function job(id: string): Job {
  return {
    id,
    kind: "issue",
    repo: "o/r",
    number: 1,
    title: "t",
    threadBody: "",
    question: "q",
    subjectNodeId: "I_1",
    htmlUrl: "",
    source: "test",
  };
}

describe("JobQueue", () => {
  it("runs jobs one at a time in order", async () => {
    const order: string[] = [];
    let running = 0;
    let maxRunning = 0;
    const queue = new JobQueue(async (j) => {
      running++;
      maxRunning = Math.max(maxRunning, running);
      await new Promise((r) => setTimeout(r, 5));
      order.push(j.id);
      running--;
    });
    expect(queue.push(job("a"))).toBe(true);
    expect(queue.push(job("b"))).toBe(true);
    expect(queue.push(job("c"))).toBe(true);
    await queue.idle();
    expect(order).toEqual(["a", "b", "c"]);
    expect(maxRunning).toBe(1);
  });

  it("drops ids it has already seen", async () => {
    const seen: string[] = [];
    const queue = new JobQueue(async (j) => {
      seen.push(j.id);
    });
    expect(queue.push(job("a"))).toBe(true);
    expect(queue.push(job("a"))).toBe(false);
    await queue.idle();
    expect(queue.push(job("a"))).toBe(false);
    expect(seen).toEqual(["a"]);
  });

  it("survives a failing handler", async () => {
    const seen: string[] = [];
    const queue = new JobQueue(async (j) => {
      seen.push(j.id);
      if (j.id === "a") throw new Error("boom");
    });
    queue.push(job("a"));
    queue.push(job("b"));
    await queue.idle();
    expect(seen).toEqual(["a", "b"]);
  });

  it("stop() finishes the job in flight and returns the rest", async () => {
    let release!: () => void;
    const started: string[] = [];
    const queue = new JobQueue(async (j) => {
      started.push(j.id);
      await new Promise<void>((r) => (release = r));
    });
    queue.push(job("a"));
    queue.push(job("b"));
    await new Promise((r) => setTimeout(r, 0));
    const stopping = queue.stop();
    release();
    const left = await stopping;
    expect(started).toEqual(["a"]);
    expect(left.map((j) => j.id)).toEqual(["b"]);
    expect(queue.push(job("c"))).toBe(false);
  });
});
