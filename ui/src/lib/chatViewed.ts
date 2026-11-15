import { derived, type Readable } from "svelte/store";
import type { PlaygroundTab } from "../stores/playground";
import type { SavedChat } from "../stores/chatHistory";

/** Owned by the mounted chat view, independently of the mobile sidebar drawer. */
export function trackViewedChat(
  history: { active: Readable<SavedChat>; setViewed(id: string | null): void },
  route: Readable<string>, tab: Readable<PlaygroundTab>, visible: Readable<boolean>,
): () => void {
  const unsubscribe = derived([history.active, route, tab, visible], ([chat, path, selected, shown]) =>
    shown && path === "/playground" && selected === "chat" ? chat.id : null,
  ).subscribe((id) => history.setViewed(id));
  return () => { unsubscribe(); history.setViewed(null); };
}
