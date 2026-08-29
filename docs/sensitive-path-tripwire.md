# The sensitive-path tripwire

A pull request that removes a safety property should not be approvable by
reflex. This is the mechanism that makes it not be.

## What it guards against

Not mistakes that break loudly - a bad provider or a malformed manifest fails
CI on its own. It guards the change that **looks routine in a diff and removes
a guarantee**: one assertion deleted from a guard test is three green lines and
a smaller file, and it is the highest-value change anybody could make here.

## The list

[`.github/sensitive-paths`](../.github/sensitive-paths) - one path per line,
the text after `#` is the reason. Adding one is a single line:

```text
scripts/ignite/internal/phases/health.go   # Gates Migrate on the cluster having actually converged.
```

A trailing `/` means the directory and everything under it; no slash means that
exact file. Blank lines and whole-line comments are ignored.

**Deliberately not YAML.** The file is read by a shell script in CI and by a Go
test, and a format needing a parser needs a dependency on both sides. `grep`
and parameter expansion do not have supply chains - which matters more than
usual here, because the obvious dependency for this job is the one that got
backdoored (see below).

`tests/go/repo/sensitivepaths_test.go` checks the file: every path must exist,
every path must carry a reason long enough to be one, and every code law in
`tests/go/repo` must be covered - discovered by reading the directory rather
than from a list, so a new guard cannot be added without protection.

**A path that no longer exists does not fail, it stops matching.** So the
rename that moves a guarded file is also the change that disarms the alarm on
it. That is why the list is tested rather than trusted.

## Why not CODEOWNERS

`.github/CODEOWNERS` already contains `* @jlemberg`, so every pull request
already requests review from the only human here. Path-specific entries would
request the same person who is already requested: **zero additional signal**,
because CODEOWNERS routes review to _different_ people and there is only one.

"Require review from Code Owners" would not add scrutiny either. It would only
bite a pull request the code owner authored, and `main` already requires one
approving review, so that constraint exists either way.

## Why a label, not a comment

A comment is scrollable-past, and after the third one it is wallpaper. The
forcing function is a **required status check that fails** until a
`sensitive-reviewed` label is applied, because applying a label is a second
deliberate act that the Approve button cannot satisfy by itself.

The comment still has a job: naming _which_ property is at risk. "This is
sensitive" tells a reviewer nothing they had not assumed. "probe.go returns a
Status and never the value it read" tells them what to look for.

## Why not `tj-actions/changed-files`

It is the obvious choice and it is the wrong one. It was backdoored in March
2025 ([CVE-2025-30066](https://github.com/advisories/ghsa-mrrh-fwg8-r2c3)) -
tags retroactively repointed at a commit that scanned runner memory for
credentials and wrote them into public logs, across 23,000+ repositories. Every
version through 45.0.7 is affected; the fix is 46.0.1.

`git diff --name-only` answers the same question with no third-party action in
the path at all, which is the right trade for a job whose entire purpose is
protecting secrets.

## The workflow

Add this by hand as `.github/workflows/sensitive-paths.yml`. The agent's App
has no `workflows` permission and a push touching that directory is rejected
server-side, which is the boundary working as intended.

The detection block below has been run against four cases - a guarded file
changed, only innocuous files changed, a decoy path (`vendor/tests/go/repo/x`,
which must **not** match), and an exact-file entry that must match only itself.

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
          set -euo pipefail
          changed=$(git diff --name-only "$BASE" "$HEAD")
          hits=""

          while IFS= read -r line; do
            path="${line%%#*}"
            path="$(printf '%s' "$path" | tr -d '[:space:]')"
            # Not `[ -z "$path" ] && continue`: that returns non-zero on the
            # common branch and trips `set -e`.
            if [ -z "$path" ]; then
              continue
            fi

            why="${line#*#}"
            why="$(printf '%s' "$why" | sed 's/^[[:space:]]*//')"
            [ "$why" = "$line" ] && why="(no reason given)"

            # Anchored, so `tests/go/repo/` is not matched by
            # `vendor/tests/go/repo/x`. Trailing slash means at-or-below;
            # no slash means that exact file.
            case "$path" in
              */) match=$(printf '%s\n' "$changed" | grep -E "^${path}" || true) ;;
              *)  match=$(printf '%s\n' "$changed" | grep -Fx "$path" || true) ;;
            esac

            if [ -n "$match" ]; then
              hits="${hits}
          ### \`${path}\`

          ${why}

          $(printf '%s\n' "$match" | sed 's/^/- `/; s/$/`/')
          "
            fi
          done <.github/sensitive-paths

          if [ -n "$hits" ]; then
            echo "tripped=true" >>"$GITHUB_OUTPUT"
            { echo 'report<<SENSITIVE_EOF'; printf '%s\n' "$hits"; echo 'SENSITIVE_EOF'; } >>"$GITHUB_OUTPUT"
          else
            echo "tripped=false" >>"$GITHUB_OUTPUT"
          fi

      - name: Say what is at risk
        if: steps.sensor.outputs.tripped == 'true'
        uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
        env:
          REPORT: ${{ steps.sensor.outputs.report }}
        with:
          script: |
            const marker = '<!-- sensitive-paths -->';
            const body = [
              marker,
              '## This pull request changes a safety property',
              '',
              'Not "important files" - properties that fail **quietly**. A deleted',
              'assertion is a smaller file and a green build.',
              '',
              process.env.REPORT,
              '',
              '---',
              'Read the reasons above, then apply the **`sensitive-reviewed`** label',
              'to clear the failing check. The label is the point: a second deliberate',
              'act that Approve cannot do on its own.',
            ].join('\n');

            // Updated in place rather than one comment per push, so the warning
            // stays a single item instead of becoming wallpaper.
            const {data: comments} = await github.rest.issues.listComments({
              issue_number: context.issue.number,
              owner: context.repo.owner, repo: context.repo.repo,
            });
            const existing = comments.find(c => c.body.includes(marker));
            if (existing) {
              await github.rest.issues.updateComment({
                comment_id: existing.id, owner: context.repo.owner,
                repo: context.repo.repo, body,
              });
            } else {
              await github.rest.issues.createComment({
                issue_number: context.issue.number, owner: context.repo.owner,
                repo: context.repo.repo, body,
              });
            }

      # The forcing function. Fails until the label is applied; the `labeled`
      # trigger above is what re-runs this when it is.
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

## Setting it up

1. Add the workflow above as `.github/workflows/sensitive-paths.yml`.
2. `gh label create sensitive-reviewed --color B60205 --description "Sensitive paths read and understood"`
3. Add **Sensitive Paths** to `main`'s required status checks.

## The honest limit

This cannot stop a determined maintainer, and it is not trying to. The only
person who can apply the label is the person who can merge, so what it buys is
**attention, not authorisation** - the alarm has to be silenced deliberately,
and the silencing is recorded on the pull request. Against the actual failure
mode here, a tired human approving their own agent's work at midnight, that is
the right shape.

Making it authorisation rather than attention needs a second human, which is a
different problem than this repository has.
