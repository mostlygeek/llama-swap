// Per-feature streaming/activity flags, ported from stores/playgroundActivity.ts.
import { observable, derived } from "./store.js";

const chatStreaming = observable(false);
const imageGenerating = observable(false);
const speechGenerating = observable(false);
const audioTranscribing = observable(false);
const rerankLoading = observable(false);

export const playgroundActivity = derived(
  [chatStreaming, imageGenerating, speechGenerating, audioTranscribing, rerankLoading],
  (chat, image, speech, audio, rerank) => chat || image || speech || audio || rerank
);

export const playgroundStores = {
  chatStreaming,
  imageGenerating,
  speechGenerating,
  audioTranscribing,
  rerankLoading,
};
