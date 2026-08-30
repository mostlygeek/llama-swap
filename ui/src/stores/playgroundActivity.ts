import { writable, derived } from "svelte/store";

const chatStreaming = writable(false);
const docsStreaming = writable(false);
const imageGenerating = writable(false);
const speechGenerating = writable(false);
const audioTranscribing = writable(false);
const rerankLoading = writable(false);

export const playgroundActivity = derived(
  [chatStreaming, docsStreaming, imageGenerating, speechGenerating, audioTranscribing, rerankLoading],
  ([$chat, $docs, $image, $speech, $audio, $rerank]) =>
    $chat || $docs || $image || $speech || $audio || $rerank
);

export const playgroundStores = {
  chatStreaming,
  docsStreaming,
  imageGenerating,
  speechGenerating,
  audioTranscribing,
  rerankLoading,
};
