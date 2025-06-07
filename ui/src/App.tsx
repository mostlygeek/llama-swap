import { BrowserRouter as Router, Routes, Route, Navigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DashboardPage from "./pages/Dashboard";
import LogViewerPage from "./pages/LogViewer";
import ModelsPage from "./pages/Models";

const queryClient = new QueryClient();

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router basename="/ui">
        <div>
          <nav className="bg-surface text-text border-b border-border p-4">
            <div className="flex items-center space-x-8">
              <h1 className="text-xl font-semibold">llama-swap</h1>
              <div className="flex space-x-4">
                <a href="/ui/" className="navlink">
                  Dashboard
                </a>
                <a href="/ui/logs" className="navlink">
                  Logs
                </a>
                <a href="/ui/models" className="navlink">
                  Models
                </a>
              </div>
            </div>
          </nav>

          <main className="max-w-7xl mx-auto py-6">
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/logs" element={<LogViewerPage />} />
              <Route path="/models" element={<ModelsPage />} />
              <Route path="*" element={<Navigate to="/ui/" replace />} />
            </Routes>
          </main>
        </div>
      </Router>
    </QueryClientProvider>
  );
}

export default App;
