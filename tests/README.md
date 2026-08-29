# Tests

This project writes tests before the code they test, so this directory is
scaffolding for work that has not happened yet as much as it is a home for
work that has. Read this before adding a test: the tier a test belongs to
decides where it lives, what it may touch, and whether a pull request can run
it at all.

## The tiers

| Tier            | Answers                                                                   | May touch                       | Runs on a PR |
| --------------- | ------------------------------------------------------------------------- | ------------------------------- | ------------ |
| **unit**        | Does this function do what it says?                                       | Nothing outside the process     | Yes          |
| **contract**    | Do two implementations of one rule agree?                                 | Files in this repository        | Yes          |
| **integration** | Does the built estate still look right, and did last night's backup work? | A real, already-built estate    | No           |
| **api**         | Does the vendor's API still behave as assumed?                            | A real vendor API               | No           |
| **e2e**         | Can this build an estate from nothing?                                    | Creates and destroys real infra | No           |

The line that matters is between the first two and the last three. Everything
above it is hermetic: no 1Password, no hypervisor, no cluster, no credentials
of any kind. That is what lets `pr-validation.yml` run it in full on a pull
request from a fork without exposing anything. Everything below it needs a
real estate, runs on the self-hosted runner, and cannot be triggered by a
pull request at all.

## Where each tier lives, and why

```text
scripts/ignite/**/*_test.go            unit + contract (Go)      — no dependencies
management/cluster/tests/*.tftest.hcl  unit (OpenTofu)           — native tofu test
management/cluster/tests/fixtures/     the shared config corpus
tests/go/repo/                         contract: the repo's own files (hermetic)
tests/go/{integration,api,e2e}/        needs a real estate — build-tagged
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

## Repository invariants, and where gotchas go

`tests/go/repo` checks this repository's own files rather than the
infrastructure they describe. It is hermetic and untagged, so `go test ./...`
in `tests/go` runs it and compiles none of the tiers that need an estate.

It exists because of one class of defect: **a key written twice, where the
parser keeps the last one and says nothing.** Every format this project
configures itself in behaves that way — an env file read by `docker
--env-file`, a JSON object decoded by `encoding/json`, a YAML mapping loaded
by the PyYAML behind pre-commit's `check-yaml`. No formatter, linter or
schema validator in this pipeline has anything to say about it.

That is not hypothetical: a merge produced a duplicated `VALIDATE_TERRAGRUNT`
in `.github/super-linter.vars`, all eleven pre-commit hooks passed the file,
and it was found only by diffing two resolutions of the same merge against
each other. The same merge duplicated a `variable "config_path"` block in
`variables.tf`, which `tofu validate` did catch — so that half stays
`tofu validate`'s to own, and this package does not repeat it.

**This is where a gotcha becomes a rule.** When something bites once and no
existing tool catches it, the fix is a check here, not only a comment
explaining the trap. Two things keep such a check honest:

1. **Test the detector, not just the repository.** A check that only ever
   asserts "everything is clean" is indistinguishable from one that is
   silently broken. Each detector has unit tests over synthetic known-bad
   input, so the check is proven to fail when it should.
2. **Make it general.** Catch the class, not the file. The duplicate-key
   check found nothing on the day it was written — it is there for the next
   merge, in whichever of the three formats that one lands in.

Why not `conftest`/OPA: it is a genuinely good tool and probably the right
answer later, for policy over Kubernetes manifests and `tofu` plans
("no container without resource limits"). It is the wrong shape here — a key
written twice is a lexical property of a file, not a policy over structured
data, and `conftest` cannot parse an env file at all. It would also add a
fifth owner of checks, and a new language, for one assertion.

### Reaching the self-hosted runner from a pull request

`repo/selfhosted_test.go` is the second rule of that kind, and it guards the
one place this repository deliberately crosses the hermetic line:
`deploy-infrastructure.yml`'s Plan job is `pull_request`-triggered and
`runs-on: self-hosted`, reading real state with real credentials.

It carries a fork guard —
`github.event.pull_request.head.repo.full_name == github.repository` — and
the useful thing to understand is that **a fork guard is not a
same-repo-branch guard.** It excludes an outside contributor. It does not
exclude a branch pushed to this repository, and that set is larger than it
looks: every collaborator, every bot with push access, and anything holding a
leaked token. Such a branch opens a pull request whose head repo _is_ this
repository, passes the guard, and runs on a machine on the estate's network.

So the check requires both the fork guard and an `environment:`, plus it
refuses `pull_request_target` on a self-hosted runner outright — that
combination hands secrets to a workflow evaluating someone else's branch, and
no `if` redeems it.

**Half of this invariant is not in the repository, and cannot be.** Whether
`staging` actually has required reviewers lives in Settings → Environments.
Worse, an environment named in a workflow but never configured is not an
error: GitHub creates it on first use with **no protection rules at all**. So
a workflow can satisfy this test completely while the gate stands open, and
the test says so in its failure message rather than implying otherwise.

The setting therefore has to be confirmed by hand, and **has been**: `staging`
and `production` both exist with required reviewers, set before any
self-hosted runner did — which is the cheap moment to do it and an awkward
one to retrofit once the runner is live.

Two things follow, and both are the reason this paragraph exists rather than
a checkbox somewhere:

- **Re-confirm it after anything that recreates an environment.** Deleting
  one and letting a workflow recreate it silently drops the protection rules,
  and no test here will notice. The check below tells you the `environment:`
  line is present; only Settings → Environments tells you it means anything.
- **`prevent_self_review` is off, deliberately.** The required reviewer is
  the repository's only human, so turning it on would make the gate
  unsatisfiable rather than stronger. It stops being the right answer the day
  a second maintainer exists, which is the point at which to revisit it.

### The vault check cannot disclose a secret

`repo/vaultcheck_test.go` guards `ignite -check-vault`. That command's whole
value is that it is safe to run and safe to share — it reports `ok` / `empty` /
`missing` per reference, and the output is meant to be pasteable into an issue
or a pull request.

That property cannot be left to the type system, because of a constraint
recorded in the epoch log: **`op` has no existence check that does not also
return the value.** Probing necessarily reads the secret. The design answer is
to make the value unreachable rather than to handle it carefully —
`onepassword.Probe` reads it into a local, measures it, and returns a `Status`
and nothing else. A caller cannot print what it was never handed.

Three rules keep that from being undone by an ordinary-looking edit:

1. `Probe` keeps its one-return-value signature. Widening it to hand back the
   value — even "just for a better error message" — removes the guarantee.
2. Nothing in the check's three files fetches or stores a secret
   (`onepassword.Read`, `Inject`, `WriteField`, `EnsureField`).
3. Nothing in them writes a file. That is what makes this the one ignite mode
   with nothing to sterilize afterwards; a file written here is a file somebody
   has to remember to delete, which is the property this project already
   decided not to rely on.

A fourth asserts the check still exists and is still driven by `Probe`, so the
three above cannot pass by guarding a function nobody calls. All four were
confirmed to fail against a deliberately broken copy before being committed —
the "test the detector" rule above.

## Where the code laws live

Three places, and the distinction is what each one can see:

| Rule about                      | Lives in                                          |
| ------------------------------- | ------------------------------------------------- |
| The repository's own files      | `tests/go/repo/`                                  |
| Two implementations of one rule | `scripts/ignite/internal/config/contract_test.go` |
| A config that must be refused   | `management/cluster/registry.tf` (preconditions)  |

`tests/go/repo` is the one to reach for when the rule is "the source must never
do X" — it reads the source as text, so it can assert things no compiler or
linter has an opinion about: the break-glass identity never appearing in
OpenTofu, no unguarded `pull_request` path to the self-hosted runner, no
credential in the Flux sync, and the vault check above.

## Running things

```sh
task test              # every hermetic tier. Seconds. No credentials.
task test:go           # Go unit + contract
task test:tofu         # OpenTofu invariants against the fixture corpus
task test:repo         # the repository's own files
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

**Tearing an estate down** is `ignite -destroy`, which is what the e2e tier
calls and what a human should call. It renders the config first - that is the
credential check, not a formality: without a 1Password session there is no
Proxmox token and no hypervisor endpoint, so somebody with a terminal and a
copy of this repository can run it all day and destroy nothing. `-confirm`
must name the site again, which guards against a typo by somebody who _does_
hold the credentials.

```sh
task destroy SITE=site0     # prints the command; does not run it
./scripts/ignite/ignite -site site0 -destroy -whatif
./scripts/ignite/ignite -site site0 -destroy -confirm site0
```

## The backup alarm

The integration tier carries one check whose ordering matters: the nightly
workflow runs the Backup phase immediately before it, then asserts against
what landed.

Asking "how old is the newest backup?" on its own would mean nothing here,
because backups otherwise happen only during an ignition run — a month-old
copy is both normal and indistinguishable from a pipeline that broke three
weeks ago. Taking one first exercises the whole path every night (pull,
encrypt, upload, confirm it landed, prune the old generation) and makes
freshness a question worth asking.

It verifies shape, not contents: that the newest object is fresh, large enough
to be real, and begins with an age header. It cannot decrypt anything, because
the age identity is deliberately offline — see
[`docs/state-and-secret-rotation.md`](../docs/state-and-secret-rotation.md).
It also reads `pg_stat_archiver` directly to confirm WAL archiving is not
currently failing, and asserts the stored generation count is still bounded,
which is how a prune that silently stopped would surface.

**The alert is the workflow failing.** GitHub already emails on that, which is
one fewer moving part than a monitoring stack — and the nightly backup doubles
as the repair: if last night's was missing or stale, tonight's replaces it.

## Running the real-estate tiers for the first time

`integration-tests.yml` has never run. Before it can, two things have to exist
that live outside this repository.

**1. A GitHub environment called `integration`.** Settings -> Environments ->
New environment. The workflow names it, and that is what gates access to the
secret below.

**2. A secret in it called `OP_SERVICE_ACCOUNT_TOKEN`.** The value is a
1Password service account token. Piping it avoids the value ever appearing in a
terminal or a shell history:

```sh
op read "op://homelab-automation/OP_SERVICE_ACCOUNT_TOKEN/credential" \
  | gh secret set OP_SERVICE_ACCOUNT_TOKEN --env integration --repo <owner>/homelab
```

> **The thing most likely to break the first run.** That service account must
> have read access to the **`homelab`** vault, not only to whichever vault the
> token itself is stored in. Every reference in `config/management.tpl.json` is
> an `op://homelab/...` path, so a token scoped to a different vault renders
> nothing and the run dies at its first step. Check before dispatching:
>
> ```sh
> OP_SERVICE_ACCOUNT_TOKEN="$(op read 'op://homelab-automation/OP_SERVICE_ACCOUNT_TOKEN/credential')" \
>   op vault list
> ```
>
> `homelab` must appear in that list.

**Then dispatch it by hand rather than waiting for 04:00.** Actions ->
Integration Tests -> Run workflow, tier `integration`. The first run is
information either way: it either proves the tier works, or it names the first
thing that does not.

Expect the API tier to need a second pass. Those tests assert on live vendor
API shapes, and the assertions were written against this project's own code
rather than against observed responses - see `tests/go/api`.

## The e2e tier

`tests/go/e2e` builds an estate from nothing and destroys it again. It is
gated two ways and passes neither of them for you:

1. A build tag, so `go test ./...` does not compile it.
2. `HOMELAB_E2E_CONFIRM` must equal `HOMELAB_TEST_SITE`, spelled out.

```sh
HOMELAB_TEST_SITE=site0 HOMELAB_E2E_CONFIRM=site0 task test:e2e
```

There was a third guard - the site must not be `site0` - written when a
disposable second estate looked likely. It is not coming, and that guard was
not making this tier safer; it was making it unrunnable, which is worse than
not having the tier at all. The estate here is disposable by design, so the
risk worth guarding is destroying the _wrong_ one, and naming the site twice is
exactly what `ignite -destroy` asks of a human. See `docs/ideas.md`.

It covers the whole ignition sequence - Render through Backup, including
moving state off local disk into cluster Postgres and pushing an encrypted
copy off-site. Teardown runs `ignite -destroy`, the same supported entrypoint
a human uses, rather than driving `tofu destroy` itself: a test that tore down
its own way would be exercising the test's teardown instead of the one that
has to work at 2am.

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
