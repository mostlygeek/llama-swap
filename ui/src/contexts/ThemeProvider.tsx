import { createContext, useContext, useEffect, type ReactNode, useMemo, useState } from "react";
import { usePersistentState } from "../hooks/usePersistentState";
import type { ConnectionState } from "../lib/types";

type ScreenWidth = "xs" | "sm" | "md" | "lg" | "xl" | "2xl";
type ThemeContextType = {
  isDarkMode: boolean;
  screenWidth: ScreenWidth;
  isNarrow: boolean;
  toggleTheme: () => void;

  // for managing the window title and connection state information
  appTitle: string;
  setAppTitle: (title: string) => void;
  setConnectionState: (state: ConnectionState) => void;
};

const ThemeContext = createContext<ThemeContextType | undefined>(undefined);

type ThemeProviderProps = {
  children: ReactNode;
};

export function ThemeProvider({ children }: ThemeProviderProps) {
  const [appTitle, setAppTitle] = usePersistentState("app-title", "llama-swap");
  const [connectionState, setConnectionState] = useState<ConnectionState>("disconnected");

  /**
   * Set the document.title with informative information
   */
  useEffect(() => {
    const connectionIcon = connectionState === "connecting" ? "🟡" : connectionState === "connected" ? "🟢" : "🔴";
    document.title = connectionIcon + " " + appTitle; // Set initial title
  }, [appTitle, connectionState]);

  const [isDarkMode, setIsDarkMode] = usePersistentState<boolean>("theme", false);
  const [screenWidth, setScreenWidth] = useState<ScreenWidth>("md"); // Default to md

  // matches tailwind classes
  // https://tailwindcss.com/docs/responsive-design
  useEffect(() => {
    const checkInnerWidth = () => {
      const innerWidth = window.innerWidth;
      if (innerWidth < 640) {
        setScreenWidth("xs");
      } else if (innerWidth < 768) {
        setScreenWidth("sm");
      } else if (innerWidth < 1024) {
        setScreenWidth("md");
      } else if (innerWidth < 1280) {
        setScreenWidth("lg");
      } else if (innerWidth < 1536) {
        setScreenWidth("xl");
      } else {
        setScreenWidth("2xl");
      }
    };

    checkInnerWidth();
    window.addEventListener("resize", checkInnerWidth);

    return () => window.removeEventListener("resize", checkInnerWidth);
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", isDarkMode ? "dark" : "light");
  }, [isDarkMode]);

  const toggleTheme = () => setIsDarkMode((prev) => !prev);
  const isNarrow = useMemo(() => {
    return screenWidth === "xs" || screenWidth === "sm" || screenWidth === "md";
  }, [screenWidth]);

  return (
    <ThemeContext.Provider
      value={{
        isDarkMode,
        toggleTheme,
        screenWidth,
        isNarrow,
        appTitle,
        setAppTitle,
        setConnectionState,
      }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextType {
  const context = useContext(ThemeContext);
  if (context === undefined) {
    throw new Error("useTheme must be used within a ThemeProvider");
  }
  return context;
}
