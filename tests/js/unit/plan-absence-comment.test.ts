import { describe, expect, it } from "vitest";
import { createRequire } from "node:module";

// The comment that says a site's plan was not produced. It runs as the last
// step of that site's plan job, when the plan failed or was cancelled - and
// since a newer push or a closed pull request cancels a plan (#438),
// "cancelled" often means "replaced", not "missing". This holds it to speaking
// only when the plan it is about was for the pull request as it stands, and in
// the same comment the plan itself uses: one per site, the latest replacing
// whatever came before (#576).
const require = createRequire(import.meta.url);
const comment = require("../../../.github/workflows/github-script/plan-absence-comment.js");

type Comment = { id: number; body: string };

function fakeGitHub(pr: { state: string; head: { sha: string } }, comments: Comment[] = []) {
  const created: string[] = [];
  const deleted: number[] = [];
  const github = {
    rest: {
      pulls: { get: async () => ({ data: pr }) },
      issues: {
        listComments: async () => ({ data: comments }),
        deleteComment: async ({ comment_id }: { comment_id: number }) => {
          deleted.push(comment_id);
          return {};
        },
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
    payload: { pull_request: { head: { sha: "abcdef0123" } } },
  };
  return { github, context, created, deleted };
}

const open = { state: "open", head: { sha: "abcdef0123" } };

describe("the plan-absence comment", () => {
  it("speaks for the site, in that site's plan comment", async () => {
    process.env.SITE = "site0";
    process.env.RESULT = "failure";
    const f = fakeGitHub(open);
    await comment(f);
    expect(f.created).toHaveLength(1);
    expect(f.created[0]?.split("\n")[0]).toBe("<!-- plan:site0 -->");
    expect(f.created[0]).toContain("No plan was produced for `abcdef0`");
    expect(f.created[0]).toContain("**failure**");
  });

  // The case that left two comments on one pull request: the site's previous
  // plan comment, written by `contractor plan` with the same first line, is
  // replaced rather than left beside the new one - and another site's is not.
  it("replaces the site's earlier plan comment and no other", async () => {
    process.env.SITE = "site0";
    process.env.RESULT = "failure";
    const f = fakeGitHub(open, [
      { id: 11, body: "<!-- plan:site0 -->\n## Plan — site0\n\nPlanned against `1234567`" },
      { id: 12, body: "<!-- plan:site1 -->\n## Plan — site1" },
    ]);
    await comment(f);
    expect(f.deleted).toEqual([11]);
    expect(f.created).toHaveLength(1);
  });

  it("stays quiet when a newer push replaced the plan", async () => {
    process.env.SITE = "site0";
    process.env.RESULT = "cancelled";
    const f = fakeGitHub({ state: "open", head: { sha: "newer" } });
    await comment(f);
    expect(f.created).toHaveLength(0);
  });

  it("stays quiet when the pull request closed", async () => {
    process.env.SITE = "site0";
    process.env.RESULT = "cancelled";
    const f = fakeGitHub({ state: "closed", head: { sha: "abcdef0123" } });
    await comment(f);
    expect(f.created).toHaveLength(0);
  });
});
