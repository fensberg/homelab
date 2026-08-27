import { defineConfig } from "vitest/config";

// .mts, not .ts: the root package.json deliberately does not declare
// "type": "module" (commitlint.config.js is CommonJS and is read by two tools
// outside this package - see package.json), so a plain .ts config is loaded as
// CommonJS and Vite warns about the ESM syntax in it. The explicit extension
// settles it per-file without changing how anything else in the repository is
// interpreted.

// Vitest owns the JavaScript/TypeScript unit and integration tiers. Playwright
// owns api and e2e - see playwright.config.ts. Two runners rather than one
// because they answer different questions: Vitest runs code in-process, and
// Playwright drives something already running over the network.
export default defineConfig({
  test: {
    // Two roots on purpose. `tests/js/**` is where tests that have no single
    // source file to sit beside live - today that is all of them, because the
    // only JavaScript in this repository is configuration. `src/**` and
    // `apps/**` are for later: when there is an application here, its unit
    // tests belong next to the code they test, which is the Node convention
    // and keeps a test from surviving the deletion of its subject.
    include: [
      "tests/js/{unit,integration}/**/*.{test,spec}.{ts,js}",
      "{src,apps,packages}/**/*.{test,spec}.{ts,js}",
    ],
    exclude: ["**/node_modules/**", "tests/go/**", "tests/js/{api,e2e}/**"],

    // A repository with no matching tests is a real state here, not an
    // error: the JavaScript tier is scaffolded ahead of the application it
    // will eventually serve.
    passWithNoTests: true,

    environment: "node",

    coverage: {
      provider: "v8",
      reporter: ["text-summary", "json-summary", "lcov"],
      reportsDirectory: "coverage/js",
      // Config files are declarations, not logic. Counting them would put a
      // coverage number on a file nothing can meaningfully exercise.
      exclude: [
        "**/node_modules/**",
        "**/*.config.{ts,js}",
        "tests/**",
        "coverage/**",
      ],
    },
  },
});
