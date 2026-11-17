# AI developer quality report

How the pull requests opened by this repository's AI developer fared
against its checks. Written by `scripts/assessor`; every number below is
computed from the GitHub API and the git history by the queries at the
end, and nothing is estimated.

- History and guards read at commit `d4f0bb48`.
- Pull requests opened 2026-08-29 to 2026-11-16.

## What was measured

| Pull requests                                       | Count |
| --------------------------------------------------- | ----- |
| In the repository                                   | 518   |
| Opened by the AI developer's account                | 419   |
| Left out: no CI run on the branch while it was open | 4     |
| With a first push that CI ran on                    | 415   |

Pull requests opened by any other account are left out: the history
cannot show who wrote them.

## 1. First-pass CI success

The first push of a pull request is the commit of the first CI run on
its branch. It passes when every check on that commit succeeded at its
first attempt. A first push whose checks were cancelled or never
finished is left out of the rate and counted in the last column.

`Sensitive Paths` is a required check that fails until a person acknowledges
the change, so no author can pass it at a first push. The second and
fourth columns leave it out.

| Opened  | Every check        | Every check but the gate | Required checks  | Required but the gate | Left out |
| ------- | ------------------ | ------------------------ | ---------------- | --------------------- | -------- |
| 2026-08 | 24 of 51 (47.1%)   | 38 of 49 (77.6%)         | none measured    | none measured         | 3        |
| 2026-09 | 95 of 162 (58.6%)  | 111 of 157 (70.7%)       | none measured    | none measured         | 44       |
| 2026-10 | 61 of 138 (44.2%)  | 63 of 138 (45.7%)        | 13 of 49 (26.5%) | 13 of 49 (26.5%)      | 1        |
| 2026-11 | 5 of 21 (23.8%)    | 6 of 21 (28.6%)          | 5 of 21 (23.8%)  | 6 of 21 (28.6%)       | 2        |
| All     | 185 of 372 (49.7%) | 218 of 365 (59.7%)       | 18 of 70 (25.7%) | 19 of 70 (27.1%)      | 50       |

Over the same pull requests as the last two columns, every check but
the gate passed 15 of 70 (21.4%).

The required columns start on 2026-10-13, the day the repository first
declared its required checks in its own code. Before that day the set
is not on record, so those pull requests are in the first two columns
only.

## 2. Failures by category

Every failed job of every pull-request run on one of the AI
developer's branches: 622 failed jobs in 479 failed runs. A further 94
failures of the job that only waits on the other lanes are left out,
because each repeats a failure already counted.

| Kind of check                                                    | Failed jobs | Share |
| ---------------------------------------------------------------- | ----------- | ----- |
| Acknowledgement gate (waits for a person)                        | 172         | 27.7% |
| Mutation proofs of the guards                                    | 83          | 13.3% |
| Plan of the infrastructure                                       | 79          | 12.7% |
| Repository guards (Go, Conftest and Semgrep rules of the estate) | 57          | 9.2%  |
| Lint aggregate (Super-Linter, retired)                           | 50          | 8.0%  |
| Unit tests and coverage floor                                    | 35          | 5.6%  |
| Reviewer (AI, retired)                                           | 28          | 4.5%  |
| Formatting (pre-commit)                                          | 22          | 3.5%  |
| Semgrep registry rules                                           | 19          | 3.1%  |
| Spelling                                                         | 19          | 3.1%  |
| Secret scan                                                      | 11          | 1.8%  |
| Validate (OpenTofu, manifests, Go build)                         | 10          | 1.6%  |
| Vulnerabilities and misconfiguration (Trivy)                     | 9           | 1.4%  |
| Shell lint                                                       | 8           | 1.3%  |
| Policy scan (Checkov)                                            | 6           | 1.0%  |
| Dependency review                                                | 4           | 0.6%  |
| Workflow scan (zizmor)                                           | 4           | 0.6%  |
| Dockerfile scan (hadolint)                                       | 3           | 0.5%  |
| Other                                                            | 2           | 0.3%  |
| Go vet                                                           | 1           | 0.2%  |

What the logs of those jobs name, counted once per failed job. 3
logs were no longer kept by GitHub and are not counted.

| Go test or guard                                               | Failed jobs |
| -------------------------------------------------------------- | ----------- |
| `TestTheLedgerProvesEachGuardFailsWhenItShould`                | 60          |
| `TestEveryUncoveredFunctionIsDeclared`                         | 26          |
| `TestTheConfigurationRulesHold`                                | 23          |
| `TestEverySelectorARuleReadsThroughIsProved`                   | 22          |
| `TestEverySilencedFindingNamesTheGuardThatProvesItMayBe`       | 14          |
| `TestAFactCrossingAModuleBoundaryIsDeclared`                   | 8           |
| `TestVMNameExamplesUseAPlaceholderSite`                        | 8           |
| `TestNoReferenceNamesAGuardThatIsGone`                         | 6           |
| `TestNoTestIsSatisfiedByAComment`                              | 5           |
| `TestEveryCheckoutRefusesToPersistCredentials`                 | 4           |
| `TestEveryDecisionIsAssertedOn`                                | 4           |
| `TestAnApplicationIsRemovedByDeletingItsDirectoryAndItsBlocks` | 3           |
| `TestEveryConfigurationRuleProvesItself`                       | 3           |
| `TestEveryRootPlansFromNothingAndKeysNoResourceByAVaultValue`  | 3           |
| `TestTheRollRefusesASiteNotFitToBeRolledAndTouchesNothing`     | 3           |

| Conftest rule                    | Failed jobs |
| -------------------------------- | ----------- |
| `offline_jobs`                   | 9           |
| `rendered_layer_variables`       | 5           |
| `rendered_platform_dependencies` | 5           |
| `supplier_actions`               | 4           |
| `suppliers`                      | 3           |
| `plan_builtin_cni_off`           | 1           |
| `plan_cluster_network`           | 1           |
| `plan_control_plane_metrics`     | 1           |
| `plan_custom_blocks`             | 1           |
| `plan_health_gate`               | 1           |

| Semgrep registry rule                                                                                   | Failed jobs |
| ------------------------------------------------------------------------------------------------------- | ----------- |
| `go.lang.security.audit.dangerous-exec-command.dangerous-exec-command`                                  | 4           |
| `trailofbits.go.iterate-over-empty-map.iterate-over-empty-map`                                          | 4           |
| `go.lang.security.decompression_bomb.potential-dos-via-decompression-bomb`                              | 3           |
| `yaml.github-actions.security.github-actions-mutable-action-tag.github-actions-mutable-action-tag`      | 2           |
| `yaml.kubernetes.security.allow-privilege-escalation.allow-privilege-escalation`                        | 2           |
| `yaml.kubernetes.security.hostnetwork-pod.hostnetwork-pod`                                              | 2           |
| `dockerfile.security.last-user-is-root.last-user-is-root`                                               | 1           |
| `yaml.kubernetes.security.allow-privilege-escalation-no-securitycontext.allow-privilege-escalation-no-` | 1           |

| Formatting hook                                 | Failed jobs |
| ----------------------------------------------- | ----------- |
| `Lint GitHub Actions workflow files`            | 7           |
| `refuse an unapproved third-party commit hook`  | 6           |
| `refuse a delivery from an unapproved supplier` | 3           |
| `fix end of files`                              | 2           |
| `prettier`                                      | 2           |

## 3. Repeat failures

A guard here is a Go test under `tests/go/repo` or a Conftest rule under
`policy/conftest/rules` whose own comment cites an issue. Its incident is
the newest issue it cites that was opened before the guard merged. A
repeat is a failed job, after the guard merged, whose log names the
guard: the same mistake made again and stopped before it merged.

A pull request that rewrites the checks can trip a guard without making
its mistake, so the rate is given as a range. The upper bound counts
every pull request opened after the guard merged. The lower bound
counts only those that changed no file under `tests/go/repo` or
`policy/conftest`.

| Measure                                                     | Value                        |
| ----------------------------------------------------------- | ---------------------------- |
| Guards whose comment cites an issue or pull request         | 54                           |
| Of those, paired with an issue opened before the guard      | 48                           |
| Upper bound: paired guards that later failed a pull request | 7                            |
| Failed jobs they account for                                | 10                           |
| Lower bound: of those, failed one that changed no check     | 0                            |
| Failed jobs they account for                                | 0                            |
| Repeat-failure rate                                         | 0.0% to 14.6% (0 to 7 of 48) |

| Guard                                                          | Incident | Guard merged | Upper | Lower | Last       |
| -------------------------------------------------------------- | -------- | ------------ | ----- | ----- | ---------- |
| `TestAnApplicationIsRemovedByDeletingItsDirectoryAndItsBlocks` | #590     | 2026-10-02   | 3     | 0     | 2026-10-18 |
| `TestAnApplicationIsNamedOnlyWhereItLives`                     | #590     | 2026-10-02   | 2     | 0     | 2026-11-12 |
| `plan_cluster_network`                                         | #578     | 2026-10-25   | 1     | 0     | 2026-10-29 |
| `plan_health_gate`                                             | #117     | 2026-10-25   | 1     | 0     | 2026-10-29 |
| `plan_image_schematic`                                         | #97      | 2026-10-25   | 1     | 0     | 2026-10-29 |
| `plan_kubeprism`                                               | #316     | 2026-10-25   | 1     | 0     | 2026-10-29 |
| `plan_talosconfig`                                             | #235     | 2026-10-25   | 1     | 0     | 2026-10-29 |

A repeat that no guard stopped cannot be counted from this data.

## 4. Before and after edits to `CLAUDE.md`

85 commits changed the file. For each, the first-pass rate (every
check but the gate) of the 20 measured pull requests opened before it
is compared with the 20 opened after. 50 edits have that many on
both sides.

| Measure                          | Value |
| -------------------------------- | ----- |
| Edits followed by a higher rate  | 22    |
| Edits followed by a lower rate   | 24    |
| Edits followed by the same rate  | 4     |
| Median change, percentage points | +0.0  |

| Month   | Edits | First-pass, every check but the gate | Repeats stopped |
| ------- | ----- | ------------------------------------ | --------------- |
| 2026-08 | 36    | 38 of 49 (77.6%)                     | 0               |
| 2026-09 | 24    | 111 of 157 (70.7%)                   | 0               |
| 2026-10 | 19    | 63 of 138 (45.7%)                    | 8               |
| 2026-11 | 6     | 6 of 21 (28.6%)                      | 2               |

This shows what happened around each edit, not what the edit caused.
Guards, lanes and the kind of work changed in the same weeks.

## 5. From incident to guard

For the 48 paired guards of section 3: the time from the issue being
opened to the guard's commit landing on the main branch.

| Measure                   | Value |
| ------------------------- | ----- |
| Median, days              | 11.9  |
| Longest, days             | 54.7  |
| Guard landed within a day | 6     |
| Within a week             | 12    |
| Later                     | 30    |

## Limits

- **Authorship.** Only pull requests opened by the AI developer's own account
  are measured. Earlier ones were opened under a person's account and are
  left out, whoever wrote them.
- **Required checks before they were declared in code** are not on record,
  and GitHub does not give the history of a ruleset to this account. Those
  pull requests are measured against every check, not against a required set.
- **A first push replaced before its checks finished** has no answer and is
  left out. Its checks show as cancelled.
- **Job logs** are kept by GitHub for a limited time. A failed job whose log
  is gone is counted by its name and contributes no test or rule name.
- **Renamed guards.** A guard is dated from the first commit that holds its
  present name. One that was renamed, or moved from Go to a rule, is dated
  from the move, and failures under its old name are not joined to it.
- **Semgrep rules of the estate's own** are not in section 3: their failures
  reach the logs under the Go test that runs them, not under a rule name.
- **A guard's comment may cite an issue that is not the incident behind it.**
  The pairing is by the number in the comment and nothing else.
- **The repeat rate is a range, not a figure.** The lower bound leaves out
  every pull request that changed a guard or a rule, which here is a large
  share of them, so it says little. The upper bound counts a pull request
  rewriting the checks as a repeat. The true rate is between them.
- **Cause.** Section 4 compares rates around an edit. It cannot separate the
  edit from everything else that changed.

## Queries used

GitHub REST API, under `/repos/<owner>/<repo>`:

```text
GET /pulls?state=all&sort=created&direction=asc     every pull request
GET /actions/runs                                   every workflow run
GET /commits/<sha>/check-runs?filter=all            checks of a first push
GET /actions/runs/<id>/jobs?filter=all              jobs of a failed run
GET /actions/jobs/<id>/logs                         log of a failed job
GET /issues/<n>                                     when an issue was opened
```

Git, at the commit named at the top:

```text
git log --format='%H %cI' --reverse <rev>
git grep -h -A80 -e 'required_checks = \[' <commit> -- '*.tf'
git ls-tree -r --name-only <rev> -- <guards> <rules>
git show <rev>:<file>
git log --format=%cI --reverse -S'func <Test>(' <rev> -- <guards>
git log --format=%cI --reverse --diff-filter=A <rev> -- <rule file>
git log --format=%cI --reverse <rev> -- <guidance file>
```

## Reproduce

```sh
task quality-report AGENT=<login>
```

The task builds `scripts/assessor` and runs it with a read-only token. The
numbers move as pull requests are opened; the commit and the dates at the
top say what a given copy was computed from.
