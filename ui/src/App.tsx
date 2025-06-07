import { BrowserRouter as Router, Routes, Route, Navigate } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Dashboard from "./components/Dashboard";
import LogViewer from "./components/LogViewer";
//import ModelList from "./components/ModelList";

const queryClient = new QueryClient();

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router basename="/ui">
        <div className="min-h-screen bg-gray-100">
          <nav className="bg-white shadow-sm border-b p-4">
            <div className="flex items-center space-x-8">
              <h1 className="text-xl font-semibold">llama-swap</h1>
              <div className="flex space-x-4">
                <a href="/ui/" className="text-gray-700 hover:text-gray-900">
                  Dashboard
                </a>
                <a href="/ui/logs" className="text-gray-700 hover:text-gray-900">
                  Logs
                </a>
                <a href="/ui/models" className="text-gray-700 hover:text-gray-900">
                  Models
                </a>
              </div>
            </div>
          </nav>

          <main className="max-w-7xl mx-auto py-6 sm:px-6 lg:px-8">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/logs" element={<LogViewer />} />
              <Route path="/models" element={<p>Placeholder</p>} />
              <Route path="*" element={<Navigate to="/ui/" replace />} />
            </Routes>
          </main>
        </div>
      </Router>
    </QueryClientProvider>
  );
}

export default App;
