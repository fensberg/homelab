import { defineConfig, devices } from "@playwright/test";

// Playwright owns the api and e2e tiers for anything reachable over HTTP.
// Vitest owns unit and integration - see vitest.config.ts.
//
// Nothing in this repository serves HTTP yet. The projects below are the shape
// the first thing that does will plug into: a Longhorn or Flux dashboard, a
// status page, an operator webhook. Both self-skip while HOMELAB_BASE_URL is
// unset, so this config is inert rather than red until then.
const baseURL = process.env.HOMELAB_BASE_URL;

export default defineConfig({
  testDir: "tests/js",
  // No tests found is a legitimate state while the tier is scaffolding.
  // Failing on it would make the lane red for a repository that simply has no
  // web surface yet.
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",

  use: {
    baseURL,
    // Cluster services sit behind the overlay network and serve their own
    // certificates. A test runner that refused them would be testing the PKI,
    // not the service.
    ignoreHTTPSErrors: true,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },

  projects: [
    {
      // API tier: HTTP assertions with no browser. Playwright's `request`
      // fixture is a full HTTP client, so this needs no browser download at
      // all - which is why CI can run this project without the ~400MB
      // browser install the e2e project needs.
      name: "api",
      testDir: "tests/js/api",
      use: {},
    },
    {
      name: "e2e",
      testDir: "tests/js/e2e",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
