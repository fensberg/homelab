# Tests

This project writes tests before the code they test, so this directory is
scaffolding for work that has not happened yet as much as it is a home for
work that has. Read this before adding a test: the tier a test belongs to
decides where it lives, what it may touch, and whether a pull request can run
it at all.

## The tiers

| Tier            | Answers                                        | May touch                       | Runs on a PR |
| --------------- | ---------------------------------------------- | ------------------------------- | ------------ |
| **unit**        | Does this function do what it says?            | Nothing outside the process     | Yes          |
| **contract**    | Do two implementations of one rule agree?      | Files in this repository        | Yes          |
| **integration** | Does the built estate still look right?        | A real, already-built estate    | No           |
| **api**         | Does the vendor's API still behave as assumed? | A real vendor API               | No           |
| **e2e**         | Can this build an estate from nothing?         | Creates and destroys real infra | No           |

The line that matters is between the first two and the last three. Everything
above it is hermetic: no 1Password, no hypervisor, no cluster, no credentials
of any kind. That is what lets `pr-validation.yml` run it in full on a pull
request from a fork without exposing anything. Everything below it needs a
real estate, runs on the self-hosted runner, and cannot be triggered by a
pull request at all.

## Where each tier lives, and why

```
scripts/ignite/**/*_test.go            unit + contract (Go)      — no dependencies
management/cluster/tests/*.tftest.hcl  unit (OpenTofu)           — native tofu test
management/cluster/tests/fixtures/     the shared config corpus
tests/go/                              integration, api, e2e     — separate module
tests/js/                              unit, integration, api, e2e (JS/TS)
tests/coverage-baseline.json           the coverage floor
```

**Go unit tests sit beside the code they test.** That is the Go convention, it
keeps a test from outliving its subject, and it is why `config_test.go` is in
`internal/config/` rather than here.

**The contract tests are in `scripts/ignite` for a reason Go forces.**
`internal/` is a per-module rule, so nothing outside `homelab/ignite` can
import `homelab/ignite/internal/config` - including this module. The contract
tests need that package, so they live there. They shell out to nothing and
read the OpenTofu source as text.

**`tests/go` is a separate Go module on purpose.** `scripts/ignite` has zero
external dependencies, and that is load-bearing: it is why the Validate lane
needs no `go.sum` cache, why Trivy's `gomod` scan of the shipped binary has
nothing to find, and why Dependabot never opens a pull request against the one
program that can destroy infrastructure. Terratest brings roughly 280
transitive modules. They stay over here.

## The config contract

`management/cluster/registry.tf` and `scripts/ignite/internal/config/config.go`
implement the same rules twice - octet bounds, vendor attestation, node
counts - so that a bad config is refused whether it arrives through the start
button or through a bare `tofu plan`. Defence in depth is only defence while
both halves say the same thing, and nothing about the languages forces that.

`management/cluster/tests/fixtures/manifest.json` is the index that makes them
agree. Every case in it is run twice: through `registry.tftest.hcl` on the HCL
side and through `contract_test.go` on the Go side. The tests also check the
corpus itself - a fixture added to one side and forgotten on the other fails
the build rather than quietly halving its own coverage.

**To add a case:** write the fixture, add an entry to `manifest.json`, and add
a `run` block of the same name to `registry.tftest.hcl`. Leaving out any of
the three is a failing test with a message telling you which one.

## Running things

```sh
task test              # every hermetic tier. Seconds. No credentials.
task test:go           # Go unit + contract
task test:tofu         # OpenTofu invariants against the fixture corpus
task test:js           # vitest + tsc
task test:coverage     # the above, with coverage checked against the floor

task test:integration  # needs a rendered config and a real estate
task test:api          # needs a rendered config and live vendor APIs
task test:e2e          # DESTRUCTIVE. See below.
```

The three below the gap read `config/management.rendered.json`, the same file
the start button reads. So the setup step is `task render-secrets` and the
teardown is `task clean-secrets` - no separate secret plumbing exists for
tests, and nothing is left on disk that ignite would not have left there.

> **A wrinkle worth knowing.** `ignite -phase render` sterilizes the workspace
> on the way out, which deletes the config it just wrote. Pass
> `-keep-on-failure` to stop it:
> `./scripts/ignite/ignite -site site0 -phase render -keep-on-failure`.
> The flag name describes the failure path rather than this one; see
> `docs/ideas.md`.

## The e2e tier

`tests/go/e2e` builds an estate from nothing and destroys it again. It is
gated three ways and passes none of them for you:

1. A build tag, so `go test ./...` does not compile it.
2. `HOMELAB_E2E_CONFIRM` must equal `HOMELAB_TEST_SITE`, spelled out.
3. The site must not be `site0`, the default in the config template.

```sh
HOMELAB_TEST_SITE=site1 HOMELAB_E2E_CONFIRM=site1 task test:e2e
```

It covers Render through Cluster - the whole build-out - and tears down from
local state. It deliberately stops before Migrate, because after that point
the state describing what to destroy lives inside the cluster being destroyed.
`EmergencyDestroy` solves that circle on the failure path; there is no
equivalent on the success path, so extending this tier through Migrate and
Backup needs ignite to grow a real teardown command first.

It is absent from CI entirely. A destructive nuke-and-pave should not be one
dropdown selection away in a web UI.

## Coverage

Report and ratchet, not a fixed threshold. A pull request may not push
coverage below the floor in `tests/coverage-baseline.json`; it is free to
leave it exactly where it found it. Raising the floor is a deliberate
committed change, so it records a decision rather than whatever the last green
run happened to hit.

A fixed threshold was considered and rejected: any number high enough to be
worth having would block the very pull requests that add the missing tests.

## The JavaScript/TypeScript tier

There is no application here yet. The toolchain is scaffolded ahead of one so
that the first thing added has somewhere to land, and so the framework
decision is not made under pressure later. Vitest owns unit and integration;
Playwright owns api and e2e. Both self-skip cleanly while there is nothing to
point them at, and `task test:js` skips entirely if Node is not installed -
a workstation that only touches the OpenTofu and Go tiers should not need it.

When an application does arrive, its unit tests belong next to its source
(`src/**/*.test.ts`), which `vitest.config.mts` already includes. `tests/js/`
is for tests with no single source file to sit beside.
