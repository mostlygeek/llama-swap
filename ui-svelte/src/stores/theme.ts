import { writable, derived, get } from "svelte/store";
import { persistentStore } from "./persistent";
import type { ScreenWidth } from "../lib/types";

// Persistent stores
export const isDarkMode = persistentStore<boolean>("theme", false);
export const appTitle = persistentStore<string>("app-title", "llama-swap");

// Non-persistent stores
export const screenWidth = writable<ScreenWidth>("md");
export const connectionState = writable<"connected" | "connecting" | "disconnected">("disconnected");

// Derived store for narrow screens
export const isNarrow = derived(screenWidth, ($screenWidth) => {
  return $screenWidth === "xs" || $screenWidth === "sm" || $screenWidth === "md";
});

// Function to toggle theme
export function toggleTheme(): void {
  isDarkMode.update((current) => !current);
}

// Function to check and update screen width
export function checkScreenWidth(): void {
  if (typeof window === "undefined") return;

  const innerWidth = window.innerWidth;
  let newWidth: ScreenWidth;

  if (innerWidth < 640) {
    newWidth = "xs";
  } else if (innerWidth < 768) {
    newWidth = "sm";
  } else if (innerWidth < 1024) {
    newWidth = "md";
  } else if (innerWidth < 1280) {
    newWidth = "lg";
  } else if (innerWidth < 1536) {
    newWidth = "xl";
  } else {
    newWidth = "2xl";
  }

  screenWidth.set(newWidth);
}

// Initialize screen width and set up resize listener
export function initTheme(): () => void {
  checkScreenWidth();

  if (typeof window !== "undefined") {
    window.addEventListener("resize", checkScreenWidth);
  }

  // Return cleanup function
  return () => {
    if (typeof window !== "undefined") {
      window.removeEventListener("resize", checkScreenWidth);
    }
  };
}

// Update document theme attribute when isDarkMode changes
export function syncThemeToDocument(): void {
  isDarkMode.subscribe((dark) => {
    if (typeof document !== "undefined") {
      document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
    }
  });
}

// Update document title when appTitle or connectionState changes
export function syncTitleToDocument(): () => void {
  const unsubTitle = appTitle.subscribe(() => updateDocumentTitle());
  const unsubConn = connectionState.subscribe(() => updateDocumentTitle());

  return () => {
    unsubTitle();
    unsubConn();
  };
}

function updateDocumentTitle(): void {
  const currentTitle = get(appTitle);
  const currentConnection = get(connectionState);

  if (typeof document !== "undefined") {
    const connectionIcon = currentConnection === "connecting" ? "\u{1F7E1}" : currentConnection === "connected" ? "\u{1F7E2}" : "\u{1F534}";
    document.title = connectionIcon + " " + currentTitle;
  }
}
