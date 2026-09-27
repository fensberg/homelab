import { describe, expect, it } from "vitest";
import { createRequire } from "node:module";

// The comment that says a management change went unplanned. It runs whenever
// the plan job did not succeed - and since a newer push or a closed pull
// request now cancels a plan (#438), "cancelled" often means "replaced", not
// "missing". This holds it to speaking only when the plan it is about was
// for the pull request as it stands.
const require = createRequire(import.meta.url);
const comment = require("../../../.github/workflows/github-script/plan-absence-comment.js");

function fakeGitHub(pr: { state: string; head: { sha: string } }) {
  const created: string[] = [];
  const github = {
    rest: {
      pulls: { get: async () => ({ data: pr }) },
      issues: {
        listComments: async () => ({ data: [] }),
        deleteComment: async () => ({}),
        createComment: async ({ body }: { body: string }) => {
          created.push(body);
          return {};
        },
      },
    },
  };
  const context = {
    repo: { owner: "example", repo: "homelab" },
    issue: { number: 7 },
    payload: { pull_request: { head: { sha: "run-head" } } },
  };
  return { github, context, created };
}

describe("the plan-absence comment", () => {
  it("speaks when the plan for the current head did not run", async () => {
    const { github, context, created } = fakeGitHub({ state: "open", head: { sha: "run-head" } });
    process.env.RESULT = "cancelled";
    await comment({ github, context });
    expect(created).toHaveLength(1);
    expect(created[0]).toContain("No plan was produced");
  });

  it("stays quiet when a newer push replaced the plan", async () => {
    const { github, context, created } = fakeGitHub({ state: "open", head: { sha: "newer" } });
    await comment({ github, context });
    expect(created).toHaveLength(0);
  });

  it("stays quiet when the pull request closed", async () => {
    const { github, context, created } = fakeGitHub({ state: "closed", head: { sha: "run-head" } });
    await comment({ github, context });
    expect(created).toHaveLength(0);
  });
});
