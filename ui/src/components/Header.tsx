import { useCallback } from "react";
import { RiMoonFill, RiSunFill } from "react-icons/ri";
import { NavLink, type NavLinkRenderProps } from "react-router-dom";
import { useTheme } from "../contexts/ThemeProvider";
import ConnectionStatusIcon from "./ConnectionStatus";

export function Header() {
  const { screenWidth, toggleTheme, isDarkMode, appTitle, setAppTitle, isNarrow } = useTheme();
  const handleTitleChange = useCallback(
    (newTitle: string) => {
      setAppTitle(newTitle.replace(/\n/g, "").trim().substring(0, 64) || "llama-swap");
    },
    [setAppTitle]
  );

  const navLinkClass = ({ isActive }: NavLinkRenderProps) =>
    `text-gray-600 hover:text-black dark:text-gray-300 dark:hover:text-gray-100 p-1 ${isActive ? "font-semibold" : ""}`;

  return (
    <header className={`flex items-center justify-between bg-surface border-b border-border px-4 ${isNarrow ? "py-1 h-[60px]" : "p-2 h-[75px]"}`}>
      {screenWidth !== "xs" && screenWidth !== "sm" && (
        <h1
          contentEditable
          suppressContentEditableWarning
          className="p-0 outline-none hover:bg-gray-100 dark:hover:bg-gray-700 rounded"
          onBlur={(e) => handleTitleChange(e.currentTarget.textContent || "(set title)")}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              handleTitleChange(e.currentTarget.textContent || "(set title)");
              e.currentTarget.blur();
            }
          }}
        >
          {appTitle}
        </h1>
      )}

      <menu className="flex items-center gap-4">
        <NavLink to="/" className={navLinkClass} type="button">
          Logs
        </NavLink>
        <NavLink to="/models" className={navLinkClass} type="button">
          Models
        </NavLink>
        <NavLink to="/activity" className={navLinkClass} type="button">
          Activity
        </NavLink>
        <button className="" onClick={toggleTheme}>
          {isDarkMode ? <RiMoonFill /> : <RiSunFill />}
        </button>
        <ConnectionStatusIcon />
      </menu>
    </header>
  );
}
