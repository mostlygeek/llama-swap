import type { Model } from "./types";

export interface GroupedModels {
  local: Model[];
  localMatching: Model[];
  peers: Model[];
}

/**
 * What a Playground tab wants from a model. Capability flags are the booleans
 * /v1/models reports (vision, reranker, ...); the modality lists come from the
 * architecture block, so a tab can ask for "takes an image in" or "makes an
 * image out" without needing a flag for every combination.
 *
 * All the fields that are set must hold. Within a field the model needs only
 * one of the listed values, except for `capabilities`, which needs all of them
 * unless `matchAny` is set.
 */
export interface ModelMatcher {
  capabilities?: string[];
  matchAny?: boolean;
  /** Model must accept at least one of these input modalities. */
  inputModalities?: string[];
  /** Model must produce at least one of these output modalities. */
  outputModalities?: string[];
}

/** One row of the model selector's dropdown. */
export interface ModelOption {
  /** The model ID or alias that gets selected. */
  value: string;
  /** Heading the row is listed under. */
  group: string;
  /** Model this row is an alias of, when it is an alias. */
  aliasOf?: string;
  /** The model's display name, when it differs from the ID. */
  name?: string;
}

export function modelServerPath(modelId: string): string {
  if (modelId === "comfyui_auto") return "/comfyui/";
  return `/upstream/${encodeURIComponent(modelId)}/`;
}

export function matchesCapabilities(model: Model, required: string[], matchAny = false): boolean {
  if (!required.length) return true;
  if (!model.capabilities) return false;
  const caps = model.capabilities as Record<string, boolean>;
  if (matchAny) {
    return required.some((cap) => caps[cap] === true);
  }
  return required.every((cap) => caps[cap] === true);
}

/** True when the matcher asks for nothing, so every model matches it. */
export function isEmptyMatcher(matcher?: ModelMatcher): boolean {
  if (!matcher) return true;
  return !matcher.capabilities?.length
    && !matcher.inputModalities?.length
    && !matcher.outputModalities?.length;
}

function hasAnyModality(reported: string[] | undefined, wanted: string[]): boolean {
  if (!wanted.length) return true;
  if (!reported?.length) return false;
  return wanted.some((modality) => reported.includes(modality));
}

/** True when the model satisfies everything the matcher asks for. */
export function matchesModel(model: Model, matcher?: ModelMatcher): boolean {
  if (!matcher || isEmptyMatcher(matcher)) return true;
  const { capabilities = [], matchAny = false, inputModalities = [], outputModalities = [] } = matcher;
  if (!matchesCapabilities(model, capabilities, matchAny)) return false;
  if (!hasAnyModality(model.modalities?.in, inputModalities)) return false;
  if (!hasAnyModality(model.modalities?.out, outputModalities)) return false;
  return true;
}

export function groupModels(models: Model[], matcher?: ModelMatcher): GroupedModels {
  const available = models.filter((m) => !m.unlisted);
  const local = available.filter((m) => !m.peerID);
  const peers = available.filter((m) => m.peerID);

  if (isEmptyMatcher(matcher)) {
    return { local, localMatching: [], peers };
  }

  const localMatching: Model[] = [];
  const localRest: Model[] = [];
  for (const model of local) {
    if (matchesModel(model, matcher)) {
      localMatching.push(model);
    } else {
      localRest.push(model);
    }
  }

  return { local: localRest, localMatching, peers };
}

/** Group headings, in the order the selector lists them. */
export const MODEL_GROUP_MATCHING = "Matching Capabilities";
export const MODEL_GROUP_PROFILE = "Profile";
export const MODEL_GROUP_SELECTORS = "Selectors";
export const MODEL_GROUP_LOCAL = "Local";
export const MODEL_GROUP_PEERS = "Peers";

function optionsFor(models: Model[], group: string, withAliases: boolean): ModelOption[] {
  const options: ModelOption[] = [];
  for (const model of models) {
    options.push({
      value: model.id,
      group,
      name: model.name && model.name !== model.id ? model.name : undefined,
    });
    if (!withAliases) continue;
    for (const alias of model.aliases ?? []) {
      options.push({ value: alias, group, aliasOf: model.id });
    }
  }
  return options;
}

/**
 * Flattens the model lists into selector rows. Models matching the tab's
 * matcher come first so the useful ones are at the top of the dropdown, then
 * the profile's pins, selectors, the rest of the local models and peers.
 */
export function buildModelOptions(args: {
  models: Model[];
  profiles?: Model[];
  selectors?: Model[];
  matcher?: ModelMatcher;
}): ModelOption[] {
  const grouped = groupModels(args.models, args.matcher);
  return [
    ...optionsFor(grouped.localMatching, MODEL_GROUP_MATCHING, true),
    ...optionsFor(args.profiles ?? [], MODEL_GROUP_PROFILE, false),
    ...optionsFor(args.selectors ?? [], MODEL_GROUP_SELECTORS, false),
    ...optionsFor(grouped.local, MODEL_GROUP_LOCAL, true),
    ...optionsFor(grouped.peers, MODEL_GROUP_PEERS, false),
  ];
}

/**
 * Filters selector rows by what was typed. Terms are matched case
 * insensitively against the model ID and its display name, and every term has
 * to match, so "qwen 30b" finds "Qwen3-30B" without caring about the order the
 * words were typed in.
 */
export function filterModelOptions(options: ModelOption[], query: string): ModelOption[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!terms.length) return options;
  return options.filter((option) => {
    const haystack = `${option.value} ${option.name ?? ""} ${option.aliasOf ?? ""}`.toLowerCase();
    return terms.every((term) => haystack.includes(term));
  });
}
