# Ideas for later

Unscheduled thoughts worth remembering, not yet worth an epoch. Move an idea
into `docs/epochs/` (as a new epoch, or a decision within an existing one)
once it's actually being worked on.

- **Use the Windows machine as an occasional/mobile compute node.** It has a
  GPU the cluster doesn't otherwise have access to. It would only be online
  intermittently, so it needs real thought about how workloads tolerate the
  node coming and going - not the same problem as a cloud spot instance,
  since a laptop can vanish for days at a time. If it works, the GPU opens up
  local AI / Hugging Face experimentation on the cluster itself.
- **Add Gemini as a second PR reviewer.** Already paying for the Google One
  tier that includes Gemini access. Could call the Gemini API directly from
  `pr-validation.yml`, or use Google's `gemini-code-assist` GitHub App.
  Pairs with the GPU-node idea above: a locally-hosted model could become a
  third independent reviewer once that's running.
- **A lower-tier environment, at least for E2E validation of manifests.** CI
  can only check that `clusters/management/` is structurally valid
  (`kustomize build` + `kubeconform`) - it cannot prove a change (a Flux
  version bump, a new controller, a config edit) actually behaves correctly
  once reconciled, since there is only ever the one real cluster. A second,
  lower-stakes cluster reconciling the same manifests first would close that
  gap: promote a change there, watch Flux actually apply and heal it, then
  promote the same change to the real cluster - the standard staging pattern,
  just not built yet for a single-cluster homelab.
- **Break the Analyze (Super-Linter) lane into dedicated per-tool jobs.**
  Go validation, ShellCheck, Trivy, Semgrep and Secrets are already their
  own lanes; checkov, ansible-lint, tflint, markdownlint, yamllint,
  PSScriptAnalyzer and zizmor are still bundled into one Super-Linter image.
  Several of today's real debugging time went straight into that bundling:
  a golangci-lint version baked into the image that didn't match this
  project's pinned Go version, Checkov silently ignoring the repo-wide
  `FILTER_REGEX_EXCLUDE` because it is one of the tools Super-Linter's own
  docs say always scans the whole workspace regardless, and a confirmed
  upstream bug in how Super-Linter hands multi-package Go diffs to
  golangci-lint. Splitting each tool into its own dedicated, individually
  pinned job would trade one shared version/config surface for eight
  smaller ones - more jobs to maintain, but each one debuggable and
  upgradable on its own, matching the pattern already used for Trivy and
  Semgrep. Coverage has to stay 1:1 with what Super-Linter currently runs;
  this is a real CI restructure (new egress allowlists per job, a rewrite
  of the CI section in the root `CLAUDE.md`), not a small tweak.

- **Give ignite a supported teardown, and an honest name for `-keep-on-failure`.**
  Two related gaps, both found while building the test tiers. First: a
  successful single-phase run sterilizes the workspace on the way out
  (`main.go`'s "belt and braces" block), which means `task render-secrets`
  deletes the config it just rendered unless `-keep-on-failure` is passed. The
  flag does the right thing; its name describes a different path, so nobody
  reaches for it. A `-keep` flag, or exempting `render` specifically, would
  make the per-phase tasks in `taskfile.yml` work as their descriptions read.
  Second: `tofu destroy` only exists on the failure route, inside
  `EmergencyDestroy`. There is no supported way to tear down an estate that
  ignited successfully - which is why `tests/go/e2e` stops before the Migrate
  phase, and which the "lower-tier environment" idea above will hit
  immediately, since a staging cluster is only cheap if it can be thrown away.
  `EmergencyDestroy` already solves the hard part (migrating state back out of
  the cluster it is about to destroy); this is mostly about exposing it.
- **Put a plan gate in front of `deploy-infrastructure.yml`.** It runs
  `tofu apply -auto-approve` on push to `main`, with no plan posted anywhere
  and no test step. Nothing has ever triggered it - `environments/` and
  `modules/` do not exist yet - so this is cheap to fix now and expensive to
  fix later. The standard shape is plan-on-PR (posted as a comment) and
  apply-on-merge against that same saved plan, so what gets applied is what
  was reviewed. Worth doing in the same epoch that first creates
  `environments/`.
- **Make the two config-contract implementations one implementation.** The
  contract tests added in the test epoch prove `registry.tf` and
  `internal/config/config.go` agree, which is a real improvement over hoping.
  Proving agreement is still second best to not having two implementations:
  the invariants could live in the OpenTofu alone, with the Go side shelling
  out to a targeted `tofu plan` for its fast pre-flight. That trades
  millisecond feedback for a subprocess and a provider directory, which is
  why it was not done now - but it is the version with no drift to detect.

- **Prove the off-site recovery path by restoring it, nightly.** Nothing in
  this repository has ever been restored, so the honest status of the recovery
  story is "unknown" rather than "working". The test framework now has the
  right shape for it: a `restore`-tagged tier on the self-hosted runner that
  pulls the latest age-encrypted state dump and the latest CloudNativePG base
  backup, restores both into a throwaway target, asserts the state parses and
  the database answers, then tears it down. It needs the disposable site above,
  which makes that idea a prerequisite rather than a nice-to-have. See
  `docs/state-and-secret-rotation.md` for the rest of the off-site hardening
  list - bucket locks, per-prefix credentials, a second vendor, and key
  custody for the age identity.
