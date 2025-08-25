import { useEffect, useCallback, type FocusEvent, type KeyboardEvent } from "react";
import { BrowserRouter as Router, Routes, Route, Navigate, NavLink } from "react-router-dom";
import { useTheme } from "./contexts/ThemeProvider";
import { useAPI } from "./contexts/APIProvider";
import LogViewerPage from "./pages/LogViewer";
import ModelPage from "./pages/Models";
import ActivityPage from "./pages/Activity";
import ConnectionStatusIcon from "./components/ConnectionStatus";
import { RiSunFill, RiMoonFill } from "react-icons/ri";
import ConfigEditor from "./pages/ConfigEditor";

function App() {
  const { isNarrow, toggleTheme, isDarkMode, appTitle, setAppTitle, setConnectionState } = useTheme();
  const handleTitleChange = useCallback(
    (newTitle: string) => {
      setAppTitle(newTitle.replace(/\n/g, "").trim().substring(0, 64) || "llama-swap");
    },
    [setAppTitle]
  );

  const { connectionStatus } = useAPI();

  // Synchronize the window.title connections state with the actual connection state
  useEffect(() => {
    setConnectionState(connectionStatus);
  }, [connectionStatus, setConnectionState]);

  return (
    <Router basename="/ui/">
      <div className="flex flex-col h-screen">
        <nav className="bg-surface border-b border-border p-2 h-[75px]">
          <div className="flex items-center justify-between mx-auto px-4 h-full">
            {!isNarrow && (
              <h1
                contentEditable
                suppressContentEditableWarning
                className="flex items-center p-0 outline-none hover:bg-gray-100 dark:hover:bg-gray-700 rounded px-1"
                onBlur={(e: FocusEvent<HTMLHeadingElement>) =>
                  handleTitleChange(e.currentTarget.textContent || "(set title)")
                }
                onKeyDown={(e: KeyboardEvent<HTMLHeadingElement>) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    handleTitleChange(e.currentTarget.textContent || "(set title)");
                    (e.currentTarget as HTMLElement).blur();
                  }
                }}
              >
                {appTitle}
              </h1>
            )}
            <div className="flex items-center space-x-4">
              <NavLink
                to="/"
                className={({ isActive }: { isActive: boolean }) => (isActive ? "navlink active" : "navlink")}
              >
                Logs
              </NavLink>
              <NavLink
                to="/models"
                className={({ isActive }: { isActive: boolean }) => (isActive ? "navlink active" : "navlink")}
              >
                Models
              </NavLink>
              <NavLink
                to="/config"
                className={({ isActive }: { isActive: boolean }) => (isActive ? "navlink active" : "navlink")}
              >
                Config
              </NavLink>
              <NavLink
                to="/activity"
                className={({ isActive }: { isActive: boolean }) => (isActive ? "navlink active" : "navlink")}
              >
                Activity
              </NavLink>
              <button className="" onClick={toggleTheme}>
                {isDarkMode ? <RiMoonFill /> : <RiSunFill />}
              </button>
              <ConnectionStatusIcon />
            </div>
          </div>
        </nav>

        <main className="flex-1 overflow-auto p-4">
          <Routes>
            <Route path="/" element={<LogViewerPage />} />
            <Route path="/models" element={<ModelPage />} />
            <Route path="/config" element={<ConfigEditor />} />
            <Route path="/activity" element={<ActivityPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </Router>
  );
}

export default App;
