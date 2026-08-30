# The sensitive-path tripwire

A pull request that removes a safety property should not be approvable by
reflex. This is the mechanism that makes it not be.

It guards the change that **looks routine in a diff**: one assertion deleted
from a guard test is three green lines and a smaller file. Things that break
loudly - a bad provider, a malformed manifest - already fail CI and need no
extra shouting.

## Three parts, and only one needs a human

| Part                                    | What it does                                   | Runs                               |
| --------------------------------------- | ---------------------------------------------- | ---------------------------------- |
| `.github/sensitive-paths`               | The list, with a reason per line               | —                                  |
| `.github/scripts/sensitive-paths.sh`    | Matches a change against it, writes the report | Called by the workflow             |
| `tests/go/repo/sensitivepaths_test.go`  | Keeps both honest                              | **Already** — inside the Test lane |
| `.github/workflows/sensitive-paths.yml` | Comments, and fails until acknowledged         | **Needs you to add it**            |

The test needs nothing from you. It runs in `task test:repo`, which is inside
the **Test (go, tofu, vitest)** lane, which is already a required status check
on `main`. The workflow is the only piece that needs adding, because the
agent's App has no `workflows` permission and a push touching that directory is
rejected server-side.

## Editing the list

One path per line, reason after `#`:

```text
scripts/steward/internal/phases/health.go   # Gates Migrate on the cluster having actually converged.
```

A trailing `/` means the directory and everything below it; no slash means that
exact file. Blank lines and whole-line comments are ignored.

**Deliberately not YAML.** The file is read by shell in CI and by a Go test, and
a format needing a parser needs a dependency on both sides. That matters more
than usual here: the obvious dependency for this job,
`tj-actions/changed-files`, is the action that was backdoored in March 2025
([CVE-2025-30066](https://github.com/advisories/ghsa-mrrh-fwg8-r2c3)) to scrape
runner memory for credentials and write them into public logs. `grep` and
parameter expansion have no supply chain.

## What the test enforces

- Every declared path **exists**. A path that no longer exists does not fail the
  tripwire - it stops matching, so the rename that moves a guarded file is also
  the change that disarms the alarm on it.
- Every path carries a **reason**, long enough to be one. "This is sensitive"
  tells a reviewer nothing they had not assumed.
- Every code law in `tests/go/repo` is **covered** - discovered by reading the
  directory, so a new guard cannot be added without protection.
- The **script itself** trips on a guarded path, stays quiet on an innocuous
  one, does not match `vendor/tests/go/repo/x` for `tests/go/repo/`, and its
  matching agrees with the Go parser for every line in the list.

That last group matters most: testing the parser alone would prove the list is
well formed while the script that reads it matched nothing, and a tripwire that
matches nothing fails silently.

## Why a label, not just a comment

A comment is scrollable-past, and after the third one it is wallpaper. The
forcing function is a **required check that fails** until a
`sensitive-reviewed` label is applied - a second deliberate act that Approve
cannot satisfy by itself.

## Why not CODEOWNERS

`.github/CODEOWNERS` already contains `* @jlemberg`, so every pull request
already requests review from the only human here. Path-specific entries would
request the same person: **zero additional signal**, because CODEOWNERS routes
review to _different_ people and there is only one. "Require review from Code
Owners" would only bite a pull request the code owner authored, and `main`
already requires an approving review either way.

## The workflow to add

Save as `.github/workflows/sensitive-paths.yml`:

```yaml
name: Sensitive Paths

on:
  pull_request:
    types: [opened, synchronize, reopened, labeled, unlabeled]

permissions:
  contents: read

jobs:
  tripwire:
    name: Sensitive Paths
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
    timeout-minutes: 5
    steps:
      - name: Harden the runner
        uses: step-security/harden-runner@05e31511f85b41b11d1cf0ef85d0992719546e2c # v2.21.0
        with:
          egress-policy: block
          allowed-endpoints: >
            api.github.com:443
            github.com:443
            objects.githubusercontent.com:443
            release-assets.githubusercontent.com:443

      - name: Checkout Code
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false

      - name: Which sensitive paths were touched
        id: sensor
        env:
          BASE: ${{ github.event.pull_request.base.sha }}
          HEAD: ${{ github.event.pull_request.head.sha }}
        run: |
          git diff --name-only "$BASE" "$HEAD" \
            | .github/scripts/sensitive-paths.sh >>"$GITHUB_OUTPUT"

      # The comment body is built entirely by the script, so changing the
      # wording never means touching this file. Copy that needs a workflow edit
      # to fix is copy that stays wrong, and the agent cannot edit workflows.
      - name: Say what is at risk
        if: steps.sensor.outputs.tripped == 'true'
        uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
        env:
          REPORT: ${{ steps.sensor.outputs.report }}
        with:
          script: |
            const body = process.env.REPORT;
            const marker = '<!-- sensitive-paths -->';
            const {data: comments} = await github.rest.issues.listComments({
              issue_number: context.issue.number,
              owner: context.repo.owner, repo: context.repo.repo,
            });
            const existing = comments.find(c => c.body.includes(marker));
            const args = {owner: context.repo.owner, repo: context.repo.repo, body};
            if (existing) {
              await github.rest.issues.updateComment({...args, comment_id: existing.id});
            } else {
              await github.rest.issues.createComment({...args, issue_number: context.issue.number});
            }

      - name: Require an explicit acknowledgement
        if: steps.sensor.outputs.tripped == 'true'
        env:
          LABELS: ${{ join(github.event.pull_request.labels.*.name, ',') }}
        run: |
          case ",$LABELS," in
            *,sensitive-reviewed,*) echo "acknowledged" ;;
            *)
              echo "::error::This pull request changes a safety property. Read the comment, then apply the 'sensitive-reviewed' label."
              exit 1 ;;
          esac
```

Then, once:

```sh
gh label create sensitive-reviewed --color B60205 \
  --description "Sensitive paths read and understood"
```

and add **Sensitive Paths** to `main`'s required status checks.

## The honest limit

The only person who can apply the label is the person who can merge, so this
buys **attention, not authorisation** - the alarm has to be silenced
deliberately, and the silencing is recorded on the pull request. Against the
actual failure mode here, a tired human approving their own agent's work at
midnight, that is the right shape. Making it authorisation needs a second
human, which is a different problem than this repository has.
