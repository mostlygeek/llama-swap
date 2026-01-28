import { writable, derived } from "svelte/store";
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
export function initScreenWidth(): () => void {
  checkScreenWidth();
  window.addEventListener("resize", checkScreenWidth);

  return () => {
    window.removeEventListener("resize", checkScreenWidth);
  };
}
