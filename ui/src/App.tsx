import { BrowserRouter as Router, Routes, Route, Navigate, NavLink } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DashboardPage from "./pages/Dashboard";
import LogViewerPage from "./pages/LogViewer";
import { useTheme } from "./contexts/ThemeProvider";

const queryClient = new QueryClient();

function App() {
  const theme = useTheme();
  return (
    <QueryClientProvider client={queryClient}>
      <Router basename="/ui">
        <div>
          <nav className="bg-surface border-b border-border p-4">
            <div className="flex items-center justify-between max-w-7xl mx-auto px-4">
              <h1>llama-swap</h1>
              <div className="flex space-x-4">
                <NavLink to="/" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Dashboard
                </NavLink>
                <NavLink to="/logs" className={({ isActive }) => (isActive ? "navlink active" : "navlink")}>
                  Logs
                </NavLink>
                <button className="btn btn--sm" onClick={theme.toggleTheme}>
                  {theme.isDarkMode ? "🌙" : "☀️"}
                </button>
              </div>
            </div>
          </nav>

          <main className="max-w-7xl mx-auto py-4 px-4">
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/logs" element={<LogViewerPage />} />
              <Route path="*" element={<Navigate to="/ui/" replace />} />
            </Routes>
          </main>
        </div>
      </Router>
    </QueryClientProvider>
  );
}

export default App;
