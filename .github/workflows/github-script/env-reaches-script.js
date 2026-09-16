// Proof that a script beside the shared action runs, and sees the caller's env.
//
// Run by .github/workflows/egress-proof.yml on every pull request. The plan
// comment scripts beside this one read their values from process.env and run
// only on infrastructure changes, so this is where a break in either half -
// the file not being readable under $/, or the calling step's env not arriving -
// is found first.
module.exports = async ({ core }) => {
  if (process.env.PROOF !== 'reached') {
    core.setFailed(
      'An env value set on the step calling $/.github/workflows/github-script ' +
        'did not reach the script. The plan comments in deploy-infrastructure.yml ' +
        'read their values this way and would post undefined.',
    );
  }
};
