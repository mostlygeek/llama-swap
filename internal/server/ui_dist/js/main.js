// App bootstrap. Replaces App.svelte + main.ts.
import { Header } from "./components/header.js";
import { startRouter } from "./router.js";
import { enableAPIEvents } from "./api.js";
import {
  initScreenWidth,
  initSystemThemeListener,
  isDarkMode,
  appTitle,
  connectionState,
} from "./theme.js";
import { PlaygroundPage } from "./pages/playground.js";
import { ModelsPage } from "./pages/models.js";
import { ModelDetailPage } from "./pages/modelDetail.js";
import { LogsPage } from "./pages/logs.js";
import { ActivityPage } from "./pages/activity.js";
import { PerformancePage } from "./pages/performance.js";
import { HardwarePage } from "./pages/hardware.js";
import { SettingsPage } from "./pages/settings.js";
import { StatsPage } from "./pages/stats.js";
import { currentPath } from "./router.js";

// /models/:id resolves the trailing segment as the model id.
function modelIdFromRoute() {
  const path = currentPath.get();
  return path.startsWith("/models/") ? decodeURIComponent(path.slice("/models/".length)) : "";
}

const routes = {
  "/": PlaygroundPage,
  "/models": ModelsPage,
  "/models/:id": () => ModelDetailPage(modelIdFromRoute),
  "/logs": LogsPage,
  "/activity": ActivityPage,
  "/performance": PerformancePage,
  "/hardware": HardwarePage,
  "/settings": SettingsPage,
  "/stats": StatsPage,
};

// data-theme attribute effect (App.svelte $effect)
isDarkMode.subscribe((dark) => {
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
});

// document.title with connection icon (App.svelte $effect)
function updateTitle() {
  const cs = connectionState.get();
  const icon = cs === "connecting" ? "\u{1F7E1}" : cs === "connected" ? "\u{1F7E2}" : "\u{1F534}";
  document.title = `${icon} ${appTitle.get()}`;
}
connectionState.subscribe(updateTitle);
appTitle.subscribe(updateTitle);

function boot() {
  const app = document.getElementById("app");

  const shell = document.createElement("div");
  shell.className = "app-shell";

  const header = Header();
  shell.appendChild(header.el);

  const main = document.createElement("main");
  main.className = "app-main";

  const playgroundContainer = document.createElement("div");
  playgroundContainer.className = "route-host";
  const routeContainer = document.createElement("div");
  routeContainer.className = "route-host";

  main.appendChild(playgroundContainer);
  main.appendChild(routeContainer);
  shell.appendChild(main);
  app.appendChild(shell);

  startRouter({
    routes,
    playgroundFactory: PlaygroundPage,
    playgroundContainer,
    routeContainer,
  });

  initScreenWidth();
  initSystemThemeListener();
  enableAPIEvents(true);
}

boot();
