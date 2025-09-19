import { useEffect } from "react";
import { Navigate, Route, BrowserRouter as Router, Routes } from "react-router-dom";
import { Header } from "./components/Header";
import { useAPI } from "./contexts/APIProvider";
import { useTheme } from "./contexts/ThemeProvider";
import ActivityPage from "./pages/Activity";
import LogViewerPage from "./pages/LogViewer";
import ModelPage from "./pages/Models";

function App() {
  const { setConnectionState } = useTheme();

  const { connectionStatus } = useAPI();

  // Synchronize the window.title connections state with the actual connection state
  useEffect(() => {
    setConnectionState(connectionStatus);
  }, [connectionStatus]);

  return (
    <Router basename="/ui/">
      <div className="flex flex-col h-screen">
        <Header />

        <main className="flex-1 overflow-auto p-4">
          <Routes>
            <Route path="/" element={<LogViewerPage />} />
            <Route path="/models" element={<ModelPage />} />
            <Route path="/activity" element={<ActivityPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </Router>
  );
}

export default App;
