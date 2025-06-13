import { BrowserRouter as Router, Routes, Route, Navigate, NavLink } from "react-router-dom";
import DashboardPage from "./pages/Dashboard";
import LogViewerPage from "./pages/LogViewer";
import { useTheme } from "./contexts/ThemeProvider";
import { APIProvider } from "./contexts/APIProvider";

function App() {
  const theme = useTheme();
  return (
    <Router basename="/ui/">
      <APIProvider>
        <div>
          <nav className="bg-surface border-b border-border p-4">
            <div className="flex items-center justify-between mx-auto px-4">
              <h1>llama-swap</h1>
              <div className="flex space-x-4">
                <NavLink to="/" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Logs
                </NavLink>

                <NavLink to="/models" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Models
                </NavLink>
                <button className="btn btn--sm" onClick={theme.toggleTheme}>
                  {theme.isDarkMode ? "🌙" : "☀️"}
                </button>
              </div>
            </div>
          </nav>

          <main className="mx-auto py-4 px-4">
            <Routes>
              <Route path="/" element={<LogViewerPage />} />
              <Route path="/models" element={<DashboardPage />} />
              <Route path="*" element={<Navigate to="/ui/" replace />} />
            </Routes>
          </main>
        </div>
      </APIProvider>
    </Router>
  );
}

export default App;
