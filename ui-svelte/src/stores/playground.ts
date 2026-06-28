import { persistentStore } from "./persistent";

export type PlaygroundTab = "chat" | "images" | "speech" | "audio" | "rerank" | "concurrency";

export const playgroundTabs: { id: PlaygroundTab; label: string }[] = [
  { id: "chat", label: "Chat" },
  { id: "images", label: "Images" },
  { id: "speech", label: "Speech" },
  { id: "audio", label: "Transcription" },
  { id: "rerank", label: "Rerank" },
  { id: "concurrency", label: "Load Test" },
];

export const selectedPlaygroundTab = persistentStore<PlaygroundTab>("playground-selected-tab", "chat");
