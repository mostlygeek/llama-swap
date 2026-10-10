import { readFile } from "node:fs/promises";

import { installFetchBase } from "../../../ui/src/cli/fetchBase";
import { askDocsAgent, loadTools } from "../../../ui/src/cli/headless";

import { runOnce as runActionEvent, loadActionEvent } from "./action";
import { Answerer, handleJob, type HandleOptions } from "./answer";
import { parseConfig, validate, USAGE, type Config } from "./config";
import { GitHubClient, GitHubError } from "./github";
import { log } from "./log";
import { runPoll } from "./poll";
import { JobQueue } from "./queue";
import { createWebhookServer, parseEvent, type ParseOptions } from "./webhook";

function die(message: string, code = 1): never {
  process.stderr.write(`error: ${message}\n`);
  process.exit(code);
}

/** Resolves on SIGTERM or SIGINT, once. */
function stopSignal(): AbortSignal {
  const controller = new AbortController();
  const onSignal = (signal: string) => {
    if (controller.signal.aborted) return;
    log("stopping", { signal });
    controller.abort();
  };
  process.once("SIGTERM", () => onSignal("SIGTERM"));
  process.once("SIGINT", () => onSignal("SIGINT"));
  return controller.signal;
}

interface Runtime {
  answerer: Answerer;
  client?: GitHubClient;
  parse: ParseOptions;
  handle: HandleOptions;
}

/** Shared startup: the agent, the GitHub client, and the login the bot must never answer. */
async function startRuntime(cfg: Config): Promise<Runtime> {
  const answerer = new Answerer({
    baseUrl: cfg.baseUrl,
    apiKey: cfg.apiKey,
    model: cfg.model as string,
    mention: cfg.mention,
    maxIterations: cfg.maxIterations,
    timeoutMs: cfg.timeoutMs,
    docsUrl: cfg.docsUrl,
  });
  await answerer.init();

  const client = cfg.token ? new GitHubClient({ token: cfg.token }) : undefined;

  let selfLogin = cfg.selfLogin;
  if (!selfLogin && client) {
    try {
      selfLogin = await client.viewerLogin();
    } catch (error) {
      // A rejected token will fail every post later; say so now. The Actions
      // token is different: it is accepted but cannot ask who it is, and its
      // comments carry type "Bot", which parseEvent skips on its own.
      if (error instanceof GitHubError && error.status === 401 && !cfg.dryRun) {
        throw new Error(`GITHUB_TOKEN was rejected: ${error.message}`);
      }
      log("could not resolve the token's login; relying on the Bot author check", {
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }
  if (selfLogin) log("self login", { login: selfLogin });

  return {
    answerer,
    client,
    parse: { mention: cfg.mention, selfLogin, repos: cfg.repos },
    handle: { answerer, client, dryRun: cfg.dryRun },
  };
}

async function cmdAsk(cfg: Config): Promise<void> {
  installFetchBase({ baseUrl: cfg.baseUrl, apiKey: cfg.apiKey });
  const tools = await loadTools(cfg.baseUrl);
  const question = cfg.positionals.join(" ").trim();
  process.stderr.write(`model ${cfg.model} @ ${cfg.baseUrl}, ${tools.length} tools\n\n`);

  const run = await askDocsAgent(
    question,
    { model: cfg.model as string, maxIterations: cfg.maxIterations, timeoutMs: cfg.timeoutMs },
    tools,
    (delta) => process.stdout.write(delta),
  );
  process.stdout.write("\n");
  for (const [i, t] of run.toolCalls.entries()) {
    process.stderr.write(`  ${i + 1}. ${t.name}(${t.args})${t.ok ? "" : "  <- tool error"}  ${t.durationMs}ms\n`);
  }
  process.stderr.write(`\n${run.iterations} iteration(s), ${(run.durationMs / 1000).toFixed(1)}s, ${run.doneReason}\n`);
  if (run.error) die(run.error);
}

async function cmdReplay(cfg: Config): Promise<void> {
  const payload = JSON.parse(await readFile(cfg.positionals[0], "utf8"));
  const rt = await startRuntime(cfg);
  const code = await runActionEvent(cfg.event as string, payload, { ...rt.parse, source: "replay" }, rt.handle);
  process.exit(code);
}

async function cmdAction(cfg: Config): Promise<void> {
  const { event, payload } = await loadActionEvent(process.env);
  const rt = await startRuntime(cfg);
  const code = await runActionEvent(event, payload, { ...rt.parse, source: "action" }, rt.handle);
  process.exit(code);
}

async function cmdServe(cfg: Config): Promise<void> {
  const rt = await startRuntime(cfg);
  const queue = new JobQueue((job) => handleJob(job, rt.handle).then(() => undefined));

  const server = createWebhookServer({
    secret: cfg.webhookSecret as string,
    path: cfg.webhookPath,
    onDelivery(event, deliveryId, payload) {
      const parsed = parseEvent(event, payload, { ...rt.parse, source: deliveryId || "webhook" });
      if ("skip" in parsed) {
        log("skipped", { event, delivery: deliveryId, reason: parsed.skip });
        return;
      }
      if (!queue.push(parsed.job)) {
        log("skipped", { event, delivery: deliveryId, reason: "already seen", job: parsed.job.id });
        return;
      }
      log("queued", { job: parsed.job.id, delivery: deliveryId, queued: queue.size });
    },
  });

  const [host, port] = cfg.listen.split(":");
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(Number(port), host, resolve);
  });
  log("listening", { listen: cfg.listen, path: cfg.webhookPath, dryRun: cfg.dryRun || undefined });

  const stopping = stopSignal();
  await new Promise<void>((resolve) => stopping.addEventListener("abort", () => resolve(), { once: true }));

  server.close();
  const left = await queue.stop();
  for (const job of left) {
    log("not answered before shutdown; redeliver it from the webhook page", { job: job.id, delivery: job.source, url: job.htmlUrl });
  }
  process.exit(0);
}

async function cmdPoll(cfg: Config): Promise<void> {
  const rt = await startRuntime(cfg);
  const stopping = stopSignal();
  await runPoll(
    {
      client: rt.client as GitHubClient,
      repos: cfg.repos,
      stateFile: cfg.stateFile,
      since: cfg.since,
      intervalMs: cfg.intervalMs,
      once: cfg.once,
      mention: cfg.mention,
      selfLogin: rt.parse.selfLogin,
      handle: rt.handle,
    },
    stopping,
  );
  process.exit(0);
}

async function main(): Promise<void> {
  let cfg: Config;
  try {
    cfg = parseConfig(process.argv.slice(2));
  } catch (error) {
    die(error instanceof Error ? error.message : String(error));
  }
  if (cfg.help) {
    process.stdout.write(USAGE);
    return;
  }
  const complaint = validate(cfg);
  if (complaint) die(`${complaint}\n\n${USAGE}`);

  switch (cfg.command) {
    case "ask":
      return cmdAsk(cfg);
    case "replay":
      return cmdReplay(cfg);
    case "action":
      return cmdAction(cfg);
    case "poll":
      return cmdPoll(cfg);
    case "serve":
      return cmdServe(cfg);
  }
}

main().catch((error) => die(error instanceof Error ? error.message : String(error)));
