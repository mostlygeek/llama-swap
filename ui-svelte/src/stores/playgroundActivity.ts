import { writable, derived } from "svelte/store";

const chatStreaming = writable(false);
const imageGenerating = writable(false);
const speechGenerating = writable(false);
const audioTranscribing = writable(false);
const rerankLoading = writable(false);

export const playgroundActivity = derived(
  [chatStreaming, imageGenerating, speechGenerating, audioTranscribing, rerankLoading],
  ([$chat, $image, $speech, $audio, $rerank]) => $chat || $image || $speech || $audio || $rerank
);

export const playgroundStores = {
  chatStreaming,
  imageGenerating,
  speechGenerating,
  audioTranscribing,
  rerankLoading,
};
