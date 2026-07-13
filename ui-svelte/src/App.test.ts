import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// App.svelte's routing/mounting behaviour requires a DOM (svelte-spa-router,
// onMount, etc.) but this project has no DOM testing environment configured
// (no jsdom/happy-dom, no @testing-library/svelte). These tests instead
// assert on the component source to guard the lazy-loading behaviour
// introduced in this change: routed pages should be dynamically imported via
// svelte-spa-router's wrap()/asyncComponent rather than statically bundled.
const appSource = readFileSync(fileURLToPath(new URL("./App.svelte", import.meta.url)), "utf-8");
const scriptSection = appSource.slice(0, appSource.indexOf("</script>"));

const LAZY_ROUTE_COMPONENTS = [
  "Activity",
  "ModelsDash",
  "ModelDetail",
  "LogViewer",
  "Settings",
  "Performance",
];

describe("App.svelte route lazy-loading", () => {
  it.each(LAZY_ROUTE_COMPONENTS)(
    "wraps the %s route in svelte-spa-router's wrap()/asyncComponent",
    (name) => {
      const pattern = new RegExp(
        `wrap\\(\\{\\s*asyncComponent:\\s*\\(\\)\\s*=>\\s*import\\("\\./routes/${name}\\.svelte"\\)\\s*\\}\\)`,
      );
      expect(appSource).toMatch(pattern);
    },
  );

  it.each(LAZY_ROUTE_COMPONENTS)(
    "does not statically import the %s route component",
    (name) => {
      expect(scriptSection).not.toMatch(new RegExp(`^\\s*import ${name} from`, "m"));
    },
  );

  it("uses the lazy-loaded Activity component for both '/' and the wildcard fallback route", () => {
    const activityImport = 'wrap({ asyncComponent: () => import("./routes/Activity.svelte") })';
    expect(appSource).toContain(`"/": ${activityImport}`);
    expect(appSource).toContain(`"*": ${activityImport}`);
  });

  it("imports the wrap helper from svelte-spa-router/wrap", () => {
    expect(scriptSection).toMatch(/import\s*\{\s*wrap\s*\}\s*from\s*"svelte-spa-router\/wrap"/);
  });

  it("keeps PlaygroundStub as an eager import mapped directly to the /playground route", () => {
    expect(scriptSection).toMatch(/import PlaygroundStub from "\.\/routes\/PlaygroundStub\.svelte"/);
    expect(appSource).toMatch(/"\/playground":\s*PlaygroundStub,/);
  });

  it("does not eagerly import the Playground component", () => {
    expect(scriptSection).not.toMatch(/^\s*import Playground from "\.\/routes\/Playground\.svelte"/m);
  });

  it("lazy-loads the Playground component on mount and assigns it once resolved", () => {
    expect(appSource).toMatch(/import\("\.\/routes\/Playground\.svelte"\)\.then\(\s*\(m\)\s*=>\s*\{/);
    expect(appSource).toMatch(/PlaygroundComponent\s*=\s*m\.default;/);
  });

  it("declares PlaygroundComponent as reactive state initialised to null", () => {
    expect(scriptSection).toMatch(/let PlaygroundComponent = \$state<Component \| null>\(null\);/);
  });

  it("conditionally renders PlaygroundComponent only once it has loaded", () => {
    expect(appSource).toMatch(/\{#if PlaygroundComponent\}\s*<PlaygroundComponent \/>\s*\{\/if\}/);
  });
});