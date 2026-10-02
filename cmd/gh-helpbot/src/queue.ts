import type { Job } from "./job";

/**
 * Runs jobs one at a time.
 *
 * The model behind the answers is a local one that serves a single request
 * well and several badly, so there is no concurrency here. Ids already seen
 * are remembered for the life of the process: GitHub redelivers a webhook
 * only on failure or by hand, and the poller has its own state file, so a
 * bounded in-memory set is enough.
 */
export type JobHandler = (job: Job) => Promise<void>;

export const SEEN_CAP = 5000;

export class JobQueue {
  private pending: Job[] = [];
  private seen = new Set<string>();
  private running: Promise<void> | undefined;
  private stopped = false;

  constructor(private readonly handler: JobHandler) {}

  /** Returns false when the job was already seen or the queue is stopped. */
  push(job: Job): boolean {
    if (this.stopped || this.seen.has(job.id)) return false;
    this.remember(job.id);
    this.pending.push(job);
    this.kick();
    return true;
  }

  private remember(id: string): void {
    this.seen.add(id);
    if (this.seen.size > SEEN_CAP) {
      const oldest = this.seen.values().next().value;
      if (oldest !== undefined) this.seen.delete(oldest);
    }
  }

  private kick(): void {
    if (this.running) return;
    this.running = this.drain().finally(() => {
      this.running = undefined;
      if (this.pending.length && !this.stopped) this.kick();
    });
  }

  private async drain(): Promise<void> {
    while (this.pending.length && !this.stopped) {
      const job = this.pending.shift() as Job;
      try {
        await this.handler(job);
      } catch {
        // The handler logs its own failures; one bad job must not stall the rest.
      }
    }
  }

  /** Waits for the job in flight (and, unless stopped, the rest of the queue). */
  async idle(): Promise<void> {
    while (this.running) await this.running;
  }

  /** Stops dequeuing. Returns the jobs that will not run, so they can be logged for redelivery. */
  async stop(): Promise<Job[]> {
    this.stopped = true;
    await this.idle();
    const left = this.pending;
    this.pending = [];
    return left;
  }

  get size(): number {
    return this.pending.length;
  }
}
