import { writable, derived } from "svelte/store";
import { persistentStore } from "./persistent";
import type { ScreenWidth } from "../lib/types";

export type ThemeMode = "light" | "dark" | "system";

function getInitialThemeMode(): ThemeMode {
  if (typeof window !== "undefined") {
    try {
      const saved = localStorage.getItem("theme");
      if (saved !== null) {
        const oldTheme = JSON.parse(saved);
        localStorage.removeItem("theme");
        return oldTheme ? "dark" : "light";
      }
    } catch (e) {
      console.error("Error parsing stored theme", e);
    }
  }
  return "system";
}

// Persistent stores
export const themeMode = persistentStore<ThemeMode>("theme-mode", getInitialThemeMode());
export const appTitle = persistentStore<string>("app-title", "llama-swap");

// Internal store for the raw OS dark preference
const systemPrefersDark = writable(
  typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches
);

// Derived store for actual dark mode state
export const isDarkMode = derived(
  [themeMode, systemPrefersDark],
  ([$themeMode, $systemPrefersDark]) => {
    if ($themeMode === "system") return $systemPrefersDark;
    return $themeMode === "dark";
  }
);

// Non-persistent stores
export const screenWidth = writable<ScreenWidth>("md");
export const connectionState = writable<"connected" | "connecting" | "disconnected">("disconnected");

// Derived store for narrow screens
export const isNarrow = derived(screenWidth, ($screenWidth) => {
  return $screenWidth === "xs" || $screenWidth === "sm" || $screenWidth === "md";
});

// Function to toggle theme
export function toggleTheme(): void {
  themeMode.update((current) => {
    if (current === "system") return "light";
    if (current === "light") return "dark";
    return "system";
  });
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

// Initialize system theme listener
export function initSystemThemeListener(): () => void {
  if (typeof window === "undefined") return () => {};

  const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
  const handleChange = (e: MediaQueryListEvent) => {
    systemPrefersDark.set(e.matches);
  };

  mediaQuery.addEventListener("change", handleChange);

  return () => {
    mediaQuery.removeEventListener("change", handleChange);
  };
}
