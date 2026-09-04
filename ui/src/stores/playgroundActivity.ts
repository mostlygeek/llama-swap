import { writable, derived } from "svelte/store";

const chatStreaming = writable(false);
const imageGenerating = writable(false);
const speechGenerating = writable(false);
const audioTranscribing = writable(false);
const rerankLoading = writable(false);

/**
 * The Docs Agent's flag. It is not part of playgroundActivity: Help is its own
 * page, so its sidebar link is the one that should show the work.
 */
export const docsAgentStreaming = writable(false);

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
