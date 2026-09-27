// Says, once, that a scheduled workflow is failing, and says it has stopped.
//
// A scheduled run's failure notifies only whoever last edited its cron, by
// email, and nobody acted on it: the expediter failed daily for a week and
// the nightly for four nights before anyone knew (#441). So each scheduled
// workflow ends with a job that runs this.
//
// One issue per workflow, found by the marker on its first line. A failure
// opens it, or updates it if it is already open; the next success closes it.
// It never comments: an outage is one notification, and the body says how
// long it has lasted and where the latest failure is, rather than a thread of
// "failed again" that teaches a reader to stop looking.
//
// Run by $/.github/workflows/github-script, which names this file rather than
// passing code in. Values arrive through process.env, set on the calling step:
// WORKFLOW (its name), FAILED ("true" or "false") and RUN_URL.
module.exports = async ({ github, context }) => {
  const workflow = process.env.WORKFLOW;
  const failed = process.env.FAILED === 'true';
  const runUrl = process.env.RUN_URL;
  const marker = `<!-- duty:${workflow} -->`;
  const repo = {owner: context.repo.owner, repo: context.repo.repo};

  const open = await github.paginate(github.rest.issues.listForRepo, {...repo, state: 'open', per_page: 100});
  const existing = open.find(i => !i.pull_request && (i.body || '').startsWith(marker));

  if (!failed) {
    if (existing) {
      await github.rest.issues.update({
        ...repo, issue_number: existing.number, state: 'closed', state_reason: 'completed',
        body: existing.body + `\n\n**Recovered:** it succeeded again on ${runUrl}.`,
      });
    }
    return;
  }

  const since = existing ? /First failure: (\S+)/.exec(existing.body)?.[1] : null;
  const body = [
    marker,
    `**${workflow}** failed on its schedule, and a scheduled failure notifies nobody else.`,
    '',
    `First failure: ${since || runUrl}`,
    `Latest failure: ${runUrl}`,
    '',
    'This issue closes itself the next time a scheduled run succeeds. Until' +
    ' then, whatever this workflow keeps true is not being kept true.',
  ].join('\n');
  if (existing) {
    await github.rest.issues.update({...repo, issue_number: existing.number, body});
  } else {
    await github.rest.issues.create({...repo, title: `${workflow} is failing on its schedule`, body});
  }
};
