import "../index.css";
import "highlight.js/styles/github-dark.css";
import TailcatApp from "./TailcatApp.svelte";
import { mount } from "svelte";

const app = mount(TailcatApp, {
  target: document.getElementById("app")!,
});

export default app;
