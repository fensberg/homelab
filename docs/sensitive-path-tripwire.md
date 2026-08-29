# The sensitive-path tripwire

A pull request that touches a safety property should not be approvable by
reflex. This is the mechanism that makes it not be.

## What it guards against

Not mistakes that break loudly - a bad provider or a malformed manifest fails
CI on its own. It guards the change that **looks routine in a diff and removes
a guarantee**: one assertion deleted from a guard test is three green lines and
a smaller file, and it is also the highest-value change anybody could make to
this repository.

The list lives in [`.github/sensitive-paths.yml`](../.github/sensitive-paths.yml),
with a reason per entry, and `tests/go/repo/sensitivepaths_test.go` checks it:
every path must exist, every entry must explain itself, and every code law in
`tests/go/repo` must be covered. That last one matters most - without it, the
newest guard would be the least protected, because adding a rule and forgetting
to cover it is the easy mistake.

**A path that no longer exists does not fail, it stops matching.** So the
rename that moves a guarded file is also the change that disarms the alarm on
it. That is why the list is tested rather than trusted.

## Why not CODEOWNERS

`.github/CODEOWNERS` already contains `* @jlemberg`, so every pull request
already requests review from the only human here. Adding path-specific entries
would request the same person who is already requested: **zero additional
signal**, because CODEOWNERS exists to route review to _different_ people and
there is only one.

Turning on "Require review from Code Owners" would not add scrutiny either. It
would only matter for a pull request that the code owner authored, and GitHub
already refuses a self-approval regardless - `main` requires one approving
review today, so that constraint exists with or without CODEOWNERS.

## Why a label, not a comment

A comment is scrollable-past, and after the third one it is wallpaper. The
forcing function is a **required status check that fails** until a label is
applied, because applying a label is a second deliberate act that the Approve
button cannot satisfy by itself. It is a speed bump rather than a wall - a solo
maintainer can always apply the label - but it cannot be done by accident, and
the label stays in the record.

The comment still has a job: naming _which_ property is at risk. "This is
sensitive" tells a reviewer nothing they had not assumed. "probe.go returns a
Status and never the value it read; widening that return type removes the
guarantee" tells them what to look for. That is why every entry carries a
reason and why the test refuses a short one.

## The workflow

This has to be added by a human: the agent's GitHub App has no `workflows`
permission, deliberately, and a push touching `.github/workflows/` is rejected
server-side. Add it as a new file, then add **Sensitive Paths** to `main`'s
required status checks.

Note what it does **not** use. `tj-actions/changed-files` is the obvious
choice and is the wrong one here: it was backdoored in March 2025
(CVE-2025-30066) to scrape secrets out of runner memory into public logs, and
the advisory covers every version through 45.0.7. `git diff` answers the same
question with no third-party action in the path at all, which is the right
trade for a job whose entire purpose is protecting secrets.

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

      - name: Checkout Code
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false

      # No third-party action. git and yq answer this, and the one action that
      # does it for you is the one that leaked secrets in 2025.
      - name: Which sensitive paths were touched
        id: sensor
        env:
          BASE: ${{ github.event.pull_request.base.sha }}
          HEAD: ${{ github.event.pull_request.head.sha }}
        run: |
          set -euo pipefail
          changed=$(git diff --name-only "$BASE" "$HEAD")
          hits=""
          while IFS= read -r p; do
            [ -z "$p" ] && continue
            match=$(printf '%s\n' "$changed" | grep -F -- "$p" || true)
            if [ -n "$match" ]; then
              why=$(yq -r ".paths[] | select(.path == \"$p\") | .why" .github/sensitive-paths.yml)
              hits="${hits}
### \`${p}\`
${match}

> ${why}
"
            fi
          done < <(yq -r '.paths[].path' .github/sensitive-paths.yml)

          if [ -n "$hits" ]; then
            echo "tripped=true" >> "$GITHUB_OUTPUT"
            { echo 'report<<SENSITIVE_EOF'; echo "$hits"; echo 'SENSITIVE_EOF'; } >> "$GITHUB_OUTPUT"
          else
            echo "tripped=false" >> "$GITHUB_OUTPUT"
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
              'to clear the failing check. The label is the point: it is a second',
              'deliberate act that Approve cannot do on its own.',
            ].join('\n');

            // Update in place rather than adding a comment per push, so the
            // warning stays one item rather than becoming wallpaper.
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

      # The forcing function. Fails until the label is applied, and the
      # `labeled` trigger above is what re-runs it when it is.
      - name: Require an explicit acknowledgement
        if: steps.sensor.outputs.tripped == 'true'
        env:
          LABELS: ${{ join(github.event.pull_request.labels.*.name, ',') }}
        run: |
          case ",$LABELS," in
            *,sensitive-reviewed,*)
              echo "acknowledged" ;;
            *)
              echo "::error::This pull request changes a safety property. Read the comment, then apply the 'sensitive-reviewed' label."
              exit 1 ;;
          esac
```

## Setting it up

1. Add the workflow above as `.github/workflows/sensitive-paths.yml`.
2. Create the label: `gh label create sensitive-reviewed --color B60205
--description "Sensitive paths read and understood"`.
3. Add **Sensitive Paths** to `main`'s required status checks, so an
   unacknowledged pull request cannot merge.
4. `yq` is present on GitHub-hosted runners; if that changes, the same parse is
   four lines of `python3 -c` with no new dependency.

## The honest limit

This cannot stop a determined maintainer, and it is not trying to. The only
person who can apply the label is the person who can merge, so the property it
buys is **attention, not authorisation** - the alarm must be silenced
deliberately and the silencing is recorded. Against the actual failure mode
here, which is a tired human clicking Approve on their own agent's work at
midnight, that is the right shape.

What it would take to make it authorisation rather than attention is a second
human, and that is a different problem than this repository has.
