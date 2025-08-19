import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.tsx";
import { ThemeProvider } from "./contexts/ThemeProvider";
import { APIProvider } from "./contexts/APIProvider";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <APIProvider>
        <App />
      </APIProvider>
    </ThemeProvider>
  </StrictMode>
);
