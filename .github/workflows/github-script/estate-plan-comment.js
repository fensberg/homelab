// One site's estate plan, written by `contractor plan`, on its pull request.
//
// Run by $/.github/workflows/github-script, which names this file rather than
// passing code in: a script handed to a shared action as an input is invisible
// to zizmor's and Semgrep's template-injection checks, while a file is read
// directly. Values arrive through process.env, set on the calling step.
module.exports = async ({ github, context }) => {
  // The body is written by `contractor plan`, not assembled here.
  // Copy that needs a workflow edit to fix is copy that stays wrong,
  // because the agent cannot edit workflows - the same reason
  // sensitive-paths.yml builds its comment in a script.
  const fs = require('fs');
  const body = fs.readFileSync('plan-comment.md', 'utf8');
  const marker = body.split('\n')[0];

  // Replace this site's previous plan rather than adding another.
  // A pull request that planned three times carried three comments
  // and the reader had to work out which one was current.
  const {data: comments} = await github.rest.issues.listComments({
    issue_number: context.issue.number,
    owner: context.repo.owner, repo: context.repo.repo,
  });
  const existing = comments.find(c => c.body.startsWith(marker));
  const repo = {owner: context.repo.owner, repo: context.repo.repo};

  // Deleted and recreated, not edited in place.
  //
  // An edited comment keeps its original position in the timeline, so
  // a plan that changed with a later push stays anchored beside the
  // commit that no longer produced it: the body reads "4 to add"
  // while sitting above the push whose plan said 6. The provenance
  // line inside the body was the first answer to this and it only
  // solves half - it tells a reader the comment is current, not where
  // the change it describes actually happened.
  //
  // Still exactly one comment per site. The delete is what keeps this
  // from becoming the append-per-push behaviour #160 describes; the
  // marker search above is what finds the one to remove.
  //
  // The cost, said out loud: a recreated comment loses any reactions
  // and any reply thread, and it notifies subscribers again. Neither
  // matters for a plan nobody replies to, and the second is arguably
  // wanted - a plan that changed is worth being told about.
  if (existing) {
    await github.rest.issues.deleteComment({...repo, comment_id: existing.id});
  }
  await github.rest.issues.createComment({...repo, issue_number: context.issue.number, body});
};
