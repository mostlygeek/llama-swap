import { BrowserRouter as Router, Routes, Route, Navigate, NavLink } from "react-router-dom";
import { useTheme } from "./contexts/ThemeProvider";
import { APIProvider } from "./contexts/APIProvider";
import LogViewerPage from "./pages/LogViewer";
import ModelPage from "./pages/Models";
import ActivityPage from "./pages/Activity";

function App() {
  const theme = useTheme();
  return (
    <Router basename="/ui/">
      <APIProvider>
        <div className="flex flex-col h-screen">
          <nav className="bg-surface border-b border-border p-2 h-[75px]">
            <div className="flex items-center justify-between mx-auto px-4 h-full">
              <h1 className="flex items-center p-0">llama-swap</h1>
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
                <button className="btn btn--sm" onClick={theme.toggleTheme}>
                  {theme.isDarkMode ? "🌙" : "☀️"}
                </button>
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
