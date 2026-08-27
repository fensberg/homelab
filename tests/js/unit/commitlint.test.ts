import { describe, expect, it } from "vitest";
import lint from "@commitlint/lint";
import load from "@commitlint/load";

// commitlint.config.js is enforced in two places: the commit-msg hook in
// .pre-commit-config.yaml, and Super-Linter's GIT_COMMITLINT validator in the
// Analyze lane. Neither of them tells you what the rules actually are - they
// only tell you, at the moment you try to commit, that you got one wrong.
//
// This is the third thing: an executable statement of what the convention
// admits and what it rejects, checked against the same config file both
// enforcement points read. It is also the JavaScript tier's proof of life -
// the one piece of JavaScript this repository has today is this config, so it
// is the one thing there is to test.
// `ignores` and `defaultIgnores` are passed through deliberately. commitlint
// applies them in lint(), not in load(), so a call that omits them silently
// tests a stricter convention than the one the hooks actually enforce - which
// is exactly what this file exists to avoid.
async function check(message: string) {
  const { rules, parserPreset, ignores, defaultIgnores } = await load(
    {},
    { cwd: process.cwd() },
  );
  return lint(message, rules, {
    ...(parserPreset?.parserOpts ? { parserOpts: parserPreset.parserOpts } : {}),
    ignores,
    defaultIgnores,
  });
}

describe("the repository's commit convention", () => {
  it.each([
    "feat: add the first test suites",
    "fix: replace the TruffleHog action with a pinned binary",
    "docs: record a deferred epoch decision",
    "chore(deps): bump the proxmox provider",
    "refactor(ignite): hoist the state database constants",
  ])("accepts %j", async (message) => {
    const report = await check(message);
    expect(report.errors, JSON.stringify(report.errors)).toHaveLength(0);
    expect(report.valid).toBe(true);
  });

  it.each([
    ["Fixed the thing", "no type prefix at all"],
    ["feat add a suite", "a type but no colon"],
    ["FEAT: add a suite", "an upper-case type"],
    ["feat:", "a type with no subject"],
  ])("rejects %j (%s)", async (message) => {
    const report = await check(message);
    expect(report.valid).toBe(false);
    expect(report.errors.length).toBeGreaterThan(0);
  });

  // The one deliberate carve-out in commitlint.config.js. Dependabot writes
  // its own commit messages and does not follow this convention; failing CI
  // over a message nobody in this repository wrote would be noise. If the
  // ignore is ever dropped, every Dependabot pull request starts failing the
  // Analyze lane - so it is worth a test that says why it exists.
  it("ignores Dependabot's own commit messages", async () => {
    const report = await check("Bump actions/checkout from 7.0.0 to 7.0.1");
    expect(
      report.valid,
      "the `ignores` carve-out in commitlint.config.js has stopped matching",
    ).toBe(true);
  });
});
