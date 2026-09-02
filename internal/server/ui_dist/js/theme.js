// Theme + layout state, ported from stores/theme.ts.
import { observable, derived, persistent } from "./store.js";

function getInitialThemeMode() {
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
  return "system";
}

export const themeMode = persistent("theme-mode", getInitialThemeMode());
export const appTitle = persistent("app-title", "llama-swap");

const prefersDarkQuery = "(prefers-color-scheme: dark)";

function getSystemPrefersDark() {
  return typeof window.matchMedia === "function" && window.matchMedia(prefersDarkQuery).matches;
}

const systemPrefersDark = observable(getSystemPrefersDark());

export const isDarkMode = derived([themeMode, systemPrefersDark], (mode, sysDark) => {
  if (mode === "system") return sysDark;
  return mode === "dark";
});

export const screenWidth = observable("md");
export const connectionState = observable("disconnected");

export const isNarrow = derived([screenWidth], (w) => w === "xs" || w === "sm" || w === "md");

export function toggleTheme() {
  themeMode.update((current) => {
    if (current === "system") return "light";
    if (current === "light") return "dark";
    return "system";
  });
}

export function checkScreenWidth() {
  const w = window.innerWidth;
  let nw;
  if (w < 640) nw = "xs";
  else if (w < 768) nw = "sm";
  else if (w < 1024) nw = "md";
  else if (w < 1280) nw = "lg";
  else if (w < 1536) nw = "xl";
  else nw = "2xl";
  screenWidth.set(nw);
}

export function initScreenWidth() {
  checkScreenWidth();
  window.addEventListener("resize", checkScreenWidth);
  return () => window.removeEventListener("resize", checkScreenWidth);
}

export function initSystemThemeListener() {
  if (typeof window.matchMedia !== "function") return () => {};
  const mq = window.matchMedia(prefersDarkQuery);
  systemPrefersDark.set(mq.matches);
  const handleChange = (e) => systemPrefersDark.set(e.matches);
  mq.addEventListener("change", handleChange);
  return () => mq.removeEventListener("change", handleChange);
}
