import { useEffect, useCallback } from "react";
import { BrowserRouter as Router, Routes, Route, Navigate, NavLink } from "react-router-dom";
import { useTheme } from "./contexts/ThemeProvider";
import { APIProvider } from "./contexts/APIProvider";
import LogViewerPage from "./pages/LogViewer";
import ModelPage from "./pages/Models";
import ActivityPage from "./pages/Activity";
import ConnectionStatus from "./components/ConnectionStatus";
import { RiSunFill, RiMoonFill } from "react-icons/ri";
import { usePersistentState } from "./hooks/usePersistentState";

function App() {
  const { isNarrow, toggleTheme, isDarkMode } = useTheme();
  const [appTitle, setAppTitle] = usePersistentState("app-title", "llama-swap");

  const handleTitleChange = useCallback(
    (newTitle: string) => {
      setAppTitle(newTitle);
      document.title = newTitle;
    },
    [setAppTitle]
  );

  useEffect(() => {
    document.title = appTitle; // Set initial title
  }, [appTitle]);

  return (
    <Router basename="/ui/">
      <APIProvider>
        <div className="flex flex-col h-screen">
          <nav className="bg-surface border-b border-border p-2 h-[75px]">
            <div className="flex items-center justify-between mx-auto px-4 h-full">
              {!isNarrow && (
                <h1
                  contentEditable
                  suppressContentEditableWarning
                  className="flex items-center p-0 outline-none hover:bg-gray-100 dark:hover:bg-gray-700 rounded px-1"
                  onBlur={(e) =>
                    handleTitleChange(e.currentTarget.textContent?.replace(/\n/g, "").trim() || "llama-swap")
                  }
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      const sanitizedText =
                        e.currentTarget.textContent?.replace(/\n/g, "").trim().substring(0, 25) || "llama-swap";
                      handleTitleChange(sanitizedText);
                      e.currentTarget.textContent = sanitizedText;
                      e.currentTarget.blur();
                    }
                  }}
                >
                  {appTitle}
                </h1>
              )}
              <div className="flex items-center space-x-4">
                <NavLink to="/" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Logs
                </NavLink>

                <NavLink to="/models" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Models
                </NavLink>

                <NavLink to="/activity" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Activity
                </NavLink>
                <button className="" onClick={toggleTheme}>
                  {isDarkMode ? <RiMoonFill /> : <RiSunFill />}
                </button>
                <ConnectionStatus />
              </div>
            </div>
          </nav>

          <main className="flex-1 overflow-auto p-4">
            <Routes>
              <Route path="/" element={<LogViewerPage />} />
              <Route path="/models" element={<ModelPage />} />
              <Route path="/activity" element={<ActivityPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </main>
        </div>
      </APIProvider>
    </Router>
  );
}

export default App;
