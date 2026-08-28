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
// `ignores`, `defaultIgnores` and `plugins` are passed through deliberately.
// commitlint applies all three in lint(), not in load(), so a call that omits
// them silently tests a different convention than the one the hooks actually
// enforce - which is exactly what this file exists to avoid.
//
// `plugins` matters most of the three, and least visibly: load() returns the
// *configuration* for a locally-defined rule but leaves its implementation in
// the plugin object. Omit it and lint() cannot resolve the rule, so a commit
// the config forbids comes back valid and the test that was supposed to prove
// otherwise passes.
async function check(message: string) {
  const { rules, parserPreset, ignores, defaultIgnores, plugins } = await load(
    {},
    { cwd: process.cwd() },
  );
  return lint(message, rules, {
    ...(parserPreset?.parserOpts ? { parserOpts: parserPreset.parserOpts } : {}),
    ignores,
    defaultIgnores,
    plugins,
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

// Claude commits to this repository under its own git identity, so a
// `Co-Authored-By: Claude` trailer credits the same party twice - GitHub
// renders the commit with an author avatar and a redundant co-author badge.
// The trailer exists to credit a contributor who is not the author.
//
// This is worth a rule rather than a note because the Claude Code harness
// instructs the model to add that trailer by default, on the assumption that
// a human is committing the model's work. That assumption is backwards here,
// so the correction has to live somewhere the next session cannot miss it.
describe("the no-self-co-authorship rule", () => {
  const subject = "fix: drop the redundant secret";

  it.each([
    ["Claude by name", "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"],
    ["a different model name", "Co-authored-by: Claude <noreply@anthropic.com>"],
    ["only the vendor domain", "Co-Authored-By: Someone <bot@anthropic.com>"],
    ["lower-cased entirely", "co-authored-by: claude <noreply@anthropic.com>"],
  ])("rejects a trailer naming %s", async (_label, trailer) => {
    const report = await check(`${subject}\n\nA body.\n\n${trailer}`);
    expect(report.valid, JSON.stringify(report.errors)).toBe(false);
    expect(report.errors.map((e) => e.name)).toContain("no-self-co-authorship");
  });

  // The rule targets self-attribution, not co-authorship. A real second
  // contributor is exactly what the trailer is for, and blocking that would
  // be trading one wrong answer for another.
  it("accepts a trailer naming a human co-author", async () => {
    const report = await check(
      `${subject}\n\nA body.\n\nCo-Authored-By: A Person <person@example.com>`,
    );
    expect(report.valid, JSON.stringify(report.errors)).toBe(true);
  });

  it("accepts a commit with no trailer at all", async () => {
    const report = await check(`${subject}\n\nA body.`);
    expect(report.valid, JSON.stringify(report.errors)).toBe(true);
  });

  // The word appears in this repository constantly - CLAUDE.md, scripts,
  // prose about the agent boundary. Only the trailer is the problem.
  it("accepts a body that merely mentions Claude", async () => {
    const report = await check(
      `${subject}\n\nCLAUDE.md says Claude runs as its own user.`,
    );
    expect(report.valid, JSON.stringify(report.errors)).toBe(true);
  });
});
