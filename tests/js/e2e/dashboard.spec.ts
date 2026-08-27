import { expect, test } from "@playwright/test";

// The browser tier: a real browser, driving a real page, against a real
// cluster. The most expensive tests in the repository and the fewest - reserve
// them for journeys that genuinely cannot be checked any other way, and let
// the API tier cover everything that is really an HTTP assertion wearing a
// browser costume.
//
//   HOMELAB_BASE_URL=https://longhorn.site0.internal pnpm test:e2e
//
// Unlike the api project, this one needs browsers installed:
//
//   pnpm exec playwright install --with-deps chromium
const baseURL = process.env.HOMELAB_BASE_URL;

test.skip(
  !baseURL,
  "HOMELAB_BASE_URL is unset - there is no dashboard to drive yet. See tests/README.md.",
);

test("the dashboard loads without a console error", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });

  const response = await page.goto("/");
  expect(response?.status(), "the dashboard did not return a page").toBeLessThan(400);

  // A rendered title is the cheapest proof that the application booted rather
  // than merely that the web server answered.
  await expect(page).toHaveTitle(/.+/);

  expect(
    consoleErrors,
    "the page rendered but logged errors; these are usually a failed asset or an API call the browser could not complete",
  ).toEqual([]);
});
