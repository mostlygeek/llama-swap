import { writable, derived } from "svelte/store";

const chatStreaming = writable(false);
const imageGenerating = writable(false);
const speechGenerating = writable(false);
const audioTranscribing = writable(false);

export const playgroundActivity = derived(
  [chatStreaming, imageGenerating, speechGenerating, audioTranscribing],
  ([$chat, $image, $speech, $audio]) => $chat || $image || $speech || $audio
);

export const playgroundStores = {
  chatStreaming,
  imageGenerating,
  speechGenerating,
  audioTranscribing,
};
