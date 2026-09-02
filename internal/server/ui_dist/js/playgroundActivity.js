// Per-feature streaming/activity flags, ported from stores/playgroundActivity.ts.
import { observable, derived } from "./store.js";

const chatStreaming = observable(false);
const imageGenerating = observable(false);
const speechGenerating = observable(false);
const audioTranscribing = observable(false);
const rerankLoading = observable(false);
const concurrencyRunning = observable(false);
const docsStreaming = observable(false);

export const playgroundActivity = derived(
  [chatStreaming, imageGenerating, speechGenerating, audioTranscribing, rerankLoading, concurrencyRunning, docsStreaming],
  (chat, image, speech, audio, rerank, concurrency, docs) =>
    chat || image || speech || audio || rerank || concurrency || docs
);

export const playgroundStores = {
  chatStreaming,
  imageGenerating,
  speechGenerating,
  audioTranscribing,
  rerankLoading,
  concurrencyRunning,
  docsStreaming,
};
