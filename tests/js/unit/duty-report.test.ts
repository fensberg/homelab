import { describe, expect, it } from "vitest";
import { createRequire } from "node:module";

// A scheduled workflow's failure is said once, kept current, and cleared on
// the next success (#441).
const require = createRequire(import.meta.url);
const report = require("../../../.github/workflows/github-script/duty-report.js");

type Issue = { number: number; body: string; pull_request?: object };

function fake(open: Issue[]) {
  const created: { title: string; body: string }[] = [];
  const updated: { issue_number: number; body?: string; state?: string }[] = [];
  const github = {
    paginate: async () => open,
    rest: {
      issues: {
        listForRepo: () => undefined,
        create: async (a: { title: string; body: string }) => {
          created.push(a);
          return {};
        },
        update: async (a: { issue_number: number; body?: string; state?: string }) => {
          updated.push(a);
          return {};
        },
      },
    },
  };
  return { github, context: { repo: { owner: "example", repo: "homelab" } }, created, updated };
}

function run(failed: boolean, url: string) {
  process.env.WORKFLOW = "Integration Tests";
  process.env.FAILED = failed ? "true" : "false";
  process.env.RUN_URL = url;
}

describe("the duty report", () => {
  it("opens one issue on the first failure", async () => {
    const f = fake([]);
    run(true, "run/1");
    await report(f);
    expect(f.created).toHaveLength(1);
    expect(f.created[0].body.startsWith("<!-- duty:Integration Tests -->")).toBe(true);
    expect(f.created[0].body).toContain("First failure: run/1");
  });

  it("updates that issue on the next failure, keeping when it started", async () => {
    const body = "<!-- duty:Integration Tests -->\nFirst failure: run/1\nLatest failure: run/1";
    const f = fake([{ number: 7, body }]);
    run(true, "run/2");
    await report(f);
    expect(f.created).toHaveLength(0);
    expect(f.updated[0].issue_number).toBe(7);
    expect(f.updated[0].body).toContain("First failure: run/1");
    expect(f.updated[0].body).toContain("Latest failure: run/2");
  });

  it("closes it on the next success", async () => {
    const f = fake([{ number: 7, body: "<!-- duty:Integration Tests -->\nFirst failure: run/1" }]);
    run(false, "run/3");
    await report(f);
    expect(f.updated[0]).toMatchObject({ issue_number: 7, state: "closed" });
    expect(f.updated[0].body).toContain("run/3");
  });

  it("does nothing on a success with nothing open, and ignores other workflows' issues", async () => {
    const f = fake([{ number: 9, body: "<!-- duty:Expedite -->\nFirst failure: x" }]);
    run(false, "run/4");
    await report(f);
    expect(f.updated).toHaveLength(0);
    run(true, "run/5");
    await report(f);
    expect(f.created).toHaveLength(1);
    expect(f.updated).toHaveLength(0);
  });
});
