import { describe, expect, it } from "vitest";
import { createRequire } from "node:module";

// The comment that says a site's plan was not produced. It runs whenever the
// plan job did not succeed - and since a newer push or a closed pull request
// now cancels a plan (#438), "cancelled" often means "replaced", not
// "missing". This holds it to speaking only when the plan it is about was for
// the pull request as it stands, only for the sites whose plan did not
// succeed, and in the same comment the plan itself uses: one per site, the
// latest replacing whatever came before.
const require = createRequire(import.meta.url);
const comment = require("../../../.github/workflows/github-script/plan-absence-comment.js");

type Comment = { id: number; body: string };

function fakeGitHub(opts: {
  pr: { state: string; head: { sha: string } };
  jobs?: { name: string; conclusion: string }[] | "unreadable";
  comments?: Comment[];
}) {
  const created: string[] = [];
  const deleted: number[] = [];
  const github = {
    paginate: async () => {
      if (opts.jobs === "unreadable") throw new Error("403");
      return opts.jobs ?? [];
    },
    rest: {
      actions: { listJobsForWorkflowRun: {} },
      pulls: { get: async () => ({ data: opts.pr }) },
      issues: {
        listComments: async () => ({ data: opts.comments ?? [] }),
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
    runId: 1,
    payload: { pull_request: { head: { sha: "abcdef0123" } } },
  };
  return { github, context, created, deleted };
}

const open = { state: "open", head: { sha: "abcdef0123" } };

describe("the plan-absence comment", () => {
  it("speaks for a site whose plan failed, in that site's plan comment", async () => {
    process.env.SITES = '["site0"]';
    const f = fakeGitHub({ pr: open, jobs: [{ name: "Plan site0", conclusion: "failure" }] });
    await comment(f);
    expect(f.created).toHaveLength(1);
    expect(f.created[0].split("\n")[0]).toBe("<!-- plan:site0 -->");
    expect(f.created[0]).toContain("No plan was produced for `abcdef0`");
    expect(f.created[0]).toContain("**failure**");
  });

  // The case that left two comments on one pull request: the site's previous
  // plan comment, written by `contractor plan` with the same first line, is
  // replaced rather than left beside the new one.
  it("replaces the site's earlier plan comment rather than adding another", async () => {
    process.env.SITES = '["site0"]';
    const f = fakeGitHub({
      pr: open,
      jobs: [{ name: "Plan site0", conclusion: "failure" }],
      comments: [
        { id: 11, body: "<!-- plan:site0 -->\n## Plan — site0\n\nPlanned against `1234567`" },
        { id: 12, body: "<!-- plan:site1 -->\n## Plan — site1" },
      ],
    });
    await comment(f);
    expect(f.deleted).toEqual([11]);
    expect(f.created).toHaveLength(1);
  });

  it("leaves a site whose plan succeeded alone", async () => {
    process.env.SITES = '["site0", "site1"]';
    const f = fakeGitHub({
      pr: open,
      jobs: [
        { name: "Plan site0", conclusion: "success" },
        { name: "Plan site1", conclusion: "cancelled" },
      ],
    });
    await comment(f);
    expect(f.created).toHaveLength(1);
    expect(f.created[0].split("\n")[0]).toBe("<!-- plan:site1 -->");
  });

  it("counts every site as unplanned when the jobs cannot be read", async () => {
    process.env.SITES = '["site0", "site1"]';
    const f = fakeGitHub({ pr: open, jobs: "unreadable" });
    await comment(f);
    expect(f.created).toHaveLength(2);
    expect(f.created[0]).toContain("did not run");
  });

  it("stays quiet when a newer push replaced the plan", async () => {
    process.env.SITES = '["site0"]';
    const f = fakeGitHub({ pr: { state: "open", head: { sha: "newer" } } });
    await comment(f);
    expect(f.created).toHaveLength(0);
  });

  it("stays quiet when the pull request closed", async () => {
    process.env.SITES = '["site0"]';
    const f = fakeGitHub({ pr: { state: "closed", head: { sha: "abcdef0123" } } });
    await comment(f);
    expect(f.created).toHaveLength(0);
  });
});
