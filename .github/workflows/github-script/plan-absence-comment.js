// Says plainly, per site, that a management change was not planned.
//
// Run by $/.github/workflows/github-script, which names this file rather than
// passing code in: a script handed to a shared action as an input is invisible
// to zizmor's and Semgrep's template-injection checks, while a file is read
// directly. Values arrive through process.env, set on the calling step.
//
// ONE COMMENT PER SITE, WHATEVER HAPPENED. This comment and the plan comment
// share the plan's marker - `<!-- plan:<site> -->`, the first line of what
// `contractor plan` writes - so whichever ran last replaces the other. They
// used to carry different markers, so a failed plan followed by a good one
// left both on the pull request, the stale one saying no plan existed.
module.exports = async ({ github, context }) => {
  const repo = {owner: context.repo.owner, repo: context.repo.repo};

  // A plan cancelled because something replaced it is not an absent plan. A
  // newer push supersedes a pull request's plan, and closing the pull request
  // cancels it; both runs end "cancelled", and neither means the change went
  // unplanned. Only a plan for the pull request's current head counts.
  const {data: pr} = await github.rest.pulls.get({...repo, pull_number: context.issue.number});
  const head = context.payload.pull_request.head.sha;
  if (pr.state !== 'open' || pr.head.sha !== head) {
    return;
  }

  // Which sites went unplanned, read from this run's own jobs. A site whose
  // job cannot be found - the list could not be read, or the job never
  // started - counts as unplanned: not knowing is not a plan.
  const sites = JSON.parse(process.env.SITES || '[]');
  let conclusions = {};
  try {
    const jobs = await github.paginate(github.rest.actions.listJobsForWorkflowRun, {
      ...repo, run_id: context.runId, filter: 'latest', per_page: 100,
    });
    for (const j of jobs) {
      conclusions[j.name] = j.conclusion;
    }
  } catch (e) {
    conclusions = {};
  }

  const {data: comments} = await github.rest.issues.listComments({...repo, issue_number: context.issue.number});

  for (const site of sites) {
    const result = conclusions['Plan ' + site];
    if (result === 'success') {
      continue;
    }
    const marker = '<!-- plan:' + site + ' -->';
    const body = [
      marker,
      '## Plan — ' + site,
      '',
      'No plan was produced for `' + head.slice(0, 7) + '`: the plan ' +
        (result ? 'ended **' + result + '**' : 'did not run') + '. The job log says why.',
      '',
      'Approving this approves the change, not what it does to the estate.',
    ].join('\n');

    // Deleted and recreated rather than edited, as the plan comment is: an
    // edited comment stays where it was first posted, beside a push that is no
    // longer the one it describes.
    for (const c of comments.filter(c => c.body.startsWith(marker))) {
      await github.rest.issues.deleteComment({...repo, comment_id: c.id});
    }
    await github.rest.issues.createComment({...repo, issue_number: context.issue.number, body});
  }
};
