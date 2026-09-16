// Says plainly that a management change was not planned against the estate.
//
// Run by $/.github/workflows/github-script, which names this file rather than
// passing code in: a script handed to a shared action as an input is invisible
// to zizmor's and Semgrep's template-injection checks, while a file is read
// directly. Values arrive through process.env, set on the calling step.
module.exports = async ({ github, context }) => {
  const marker = '<!-- plan-absence -->';
  const body = [
    marker,
    '## No plan was produced for this change',
    '',
    'This pull request changes `management/`, and the plan job ended as' +
    ' **`' + process.env.RESULT + '`** rather than succeeding. Nothing here' +
    ' compared the proposed configuration against the estate that exists.',
    '',
    'That is not a blocker and is not meant to be. The plan runs on a runner' +
    ' inside the cluster, so when the cluster is down no plan can be produced -' +
    ' which is exactly when the pull request rebuilding it needs to merge.' +
    ' Requiring a plan would deadlock that.',
    '',
    'What it means for a reviewer: approving this is approving HCL rather than' +
    ' its consequences. If the estate is up, re-running the plan job is worth' +
    ' more than reading the diff again.',
  ].join('\n');

  // One comment per pull request, replaced rather than appended. The
  // workload plan comment already does this; the estate plan comment
  // does not, which is #160.
  const {data: comments} = await github.rest.issues.listComments({
    issue_number: context.issue.number,
    owner: context.repo.owner, repo: context.repo.repo,
  });
  const existing = comments.find(c => c.body.startsWith(marker));
  const repo = {owner: context.repo.owner, repo: context.repo.repo};

  // Deleted and recreated, for the reason given on the plan comment
  // above: an edited comment stays where it was first posted, which
  // is not where the push it describes happened.
  if (existing) {
    await github.rest.issues.deleteComment({...repo, comment_id: existing.id});
  }
  await github.rest.issues.createComment({...repo, issue_number: context.issue.number, body});
};
