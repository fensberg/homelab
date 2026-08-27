import { expect, test } from "@playwright/test";

// The API tier for anything this cluster serves over HTTP.
//
// Nothing does yet, so this file is the shape rather than the substance: it is
// what a real check looks like here, and it skips cleanly until there is
// something to point it at. Set HOMELAB_BASE_URL to a service reachable from
// the runner - over the overlay network, since nothing is exposed publicly.
//
//   HOMELAB_BASE_URL=https://longhorn.site0.internal pnpm test:api
//
// Playwright's `request` fixture is a full HTTP client, so this project needs
// no browser installed at all - which is why CI can run it without the
// browser download the e2e project requires.
const baseURL = process.env.HOMELAB_BASE_URL;

test.skip(
  !baseURL,
  "HOMELAB_BASE_URL is unset - there is no service to check yet. See tests/README.md.",
);

test("the service answers and identifies itself", async ({ request }) => {
  const response = await request.get("/");

  expect(
    response.status(),
    `${baseURL} answered ${response.status()}. A 401 or 403 means the request reached the service but was not authorised; a timeout means the overlay route is not up.`,
  ).toBeLessThan(400);

  // Assert on a header rather than page content: content is the application's
  // to change, while "it is HTTP and it is served by something we recognise"
  // is the contract this tier is actually here to hold.
  expect(response.headers()).toHaveProperty("content-type");
});

test("the service is served over TLS", async ({ request }) => {
  expect(
    baseURL,
    "every service in this cluster is reached over the overlay network and should be TLS-terminated; a plain-HTTP base URL is a finding, not a configuration choice",
  ).toMatch(/^https:/);

  const response = await request.get("/");
  expect(response.url()).toMatch(/^https:/);
});
