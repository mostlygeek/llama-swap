import "./index.css";
import "highlight.js/styles/github-dark.css";
import "katex/dist/katex.min.css";
import App from "./App.svelte";
import { mount } from "svelte";

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
