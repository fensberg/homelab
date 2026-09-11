# Prior art: ionfury/homelab

A comparison against [ionfury/homelab](https://github.com/ionfury/homelab), read
at commit `150097e` (2026-09-11), and what this estate could take from it.

It is worth reading closely because it is the nearest neighbour this estate has.
Talos, OpenTofu, Flux, Cilium with kube-proxy replacement, CloudNativePG, a
`versions.env` with Renovate annotations, and a Valheim server - on the same
stack, about seventeen hundred pull requests further along. Its latest commit
the day this was read was a fix for the same Valheim 1.0 save-format change this
estate hit on the same day.

Nothing here is a plan. Each section says what they do, whether it fits, what it
would close here, and what it would cost - in particular whether it brings a new
supplier, which by this estate's rule needs its own pull request.

## The direction each verdict is judged against

Taken from the open issues, the epoch records and
[`environments-and-promotion.md`](../environments-and-promotion.md), so the
verdicts are measured against something written down rather than taste:

- **Promote an immutable artefact, and make the promotion pull request
  generated** rather than hand-typed (#358, #375).
- **Production changes by a reviewed pin**, and the human decision stays human.
- **One place for every version** (#334, #339).
- **Workload secrets out of the ignition tier** and into a store the cluster
  reads (#345).
- **Workload data survives the cluster** (#372).
- **Isolation proportional to exposure and blast radius**, default-deny.
- **Fail closed, pin everything, reach for the ecosystem's answer, no ClickOps.**

## Summary

| Pattern                                             | Verdict      | Would close                     | New supplier     |
| --------------------------------------------------- | ------------ | ------------------------------- | ---------------- |
| In-place Talos and Kubernetes upgrades (Tuppr)      | Adopt        | #97, most of #372's risk        | Yes              |
| One versions file read by everything, plus Renovate | Adopt        | #334, #339, #270, #375          | Yes (Renovate)   |
| Velero file-system backup, proved by a round trip   | Adopt        | #372                            | Yes              |
| Secrets classified by persistence and origin, ESO   | Adopt        | #345                            | Yes              |
| Restart when configuration changes                  | Adopt        | a recurring silent failure      | Only for Secrets |
| Promote an artefact of the whole Flux tree          | Adapt        | #357, #358                      | No               |
| Network policy chosen by namespace profile          | Adapt        | onboarding friction             | No               |
| A separate root for what outlives the cluster       | Adapt        | the forget-and-adopt workaround | No               |
| Probes that mean "serving"                          | Adapt        | `rollout status` lying          | No               |
| Steam backend behind a LAN LoadBalancer             | Do not adopt |                                 |                  |
| A game image that installs the game at runtime      | Do not adopt |                                 |                  |
| Automerge straight through to production            | Do not adopt |                                 |                  |

## Adopt

### 1. Talos and Kubernetes upgrade in place, from inside the cluster

**What they do.** [Tuppr](https://github.com/home-operations/tuppr) is a small
controller. A `TalosUpgrade` and a `KubernetesUpgrade` resource each name a
version - substituted from `versions.env` - a maintenance window, and a list of
health checks written as CEL expressions over live resources. Bump the version
in git; the controller rolls the nodes one at a time inside the window, and
refuses to continue while any check is false. Their checks are the same questions
this estate's Health phase asks: every node Ready, the CloudNativePG cluster at
its full instance count, storage healthy.

**Why it matters here more than anywhere else in this document.** The object
storage file says it plainly: a rebuild is the routine way a Talos version
reaches these machines (#97). That was acceptable while the cluster was empty. It
stopped being acceptable the moment a world with players' buildings in it landed
on an OpenEBS hostpath volume, because hostpath data lives on one worker's disk
and a demolish takes the disk. Every Talos patch would currently mean taking an
off-site copy of the world by hand first, and that is the kind of step that is
remembered right up until it is not.

An in-place upgrade is meant to keep each node's data partition, which is where
OpenEBS hostpath volumes live, so the volume would survive. That would turn
upgrades from the most dangerous routine operation here into an ordinary one,
and it is why this is first - **but that property is the thing to prove, not
assume.** Whether a Talos upgrade preserves the ephemeral partition has been a
flag rather than a guarantee across releases, and getting it wrong is exactly
the loss this section exists to prevent.

**How it compares with what was already named.** `02-abstraction.md` names
Cluster API with the Proxmox provider as the genuine answer for node lifecycle.
The two solve different things. Cluster API replaces machines declaratively;
Tuppr upgrades the machines you have. For a single hypervisor where the goal is
"patch Talos without losing a disk", Tuppr is the smaller tool that fits, and it
does not preclude Cluster API later.

**Cost, and what to verify before trusting it.**

- **That the data partition survives.** Seed a sentinel file on a hostpath
  volume, upgrade that node through Tuppr, and check the sentinel afterwards -
  before the world is ever on a node being upgraded. This is the round trip
  from section 3, applied to upgrades.
- A new supplier (`home-operations`), so a dedicated pull request.
- This estate's image is a Factory image with a schematic carrying the Tailscale
  extension, and the extension version follows `talos_version`. An in-place
  upgrade must keep the schematic, or the nodes come back without the overlay.
  Verify that before the first real upgrade rather than during it.
- OpenTofu also knows the Talos version, for building new machines. Both must
  read the same value (section 2), or the next ignition quietly downgrades.

### 2. One versions file that everything reads, and Renovate driving it

**What they do.** `kubernetes/platform/versions.env` is described in its own
header as the single source of truth, and it genuinely is: Terragrunt reads it
for Talos, Kubernetes and the Cilium bootstrap; Flux substitutes it into chart
versions; Tuppr reads it for upgrades; Renovate updates it. Each line carries a
`# renovate:` annotation, and one generic regex manager in `.github/renovate.json5`
parses every such line - the same annotation format this estate already writes
in seven places, for a Renovate that does not run.

Renovate itself runs **self-hosted in GitHub Actions**, hourly, authenticating
with a dedicated GitHub App through `actions/create-github-app-token`. That is
the shape #375 leaned towards. Patch, minor and digest updates automerge only
after a soak - `minimumReleaseAge` of one day for patches, three for minors - and
majors never do.

**Version holds** are the part worth copying even before the rest. When an
upstream release regresses, the downgrade goes into `versions.env` and an entry
goes into `.github/version-holds.yaml` recording the constraint, the reason, and
the upstream issue. A weekly workflow checks each upstream issue and opens an
issue here when it closes. A hold that cannot be forgotten is the fail-closed
version of a comment saying "pinned because of a bug".

**What it closes here.** #339 (the Talos version pinned outside
`versions.env`), #334 (suppliers and versions in one place), #270 (chart pins
annotated for a Renovate that does not run), and the third-party half of #375.

**What this estate must do differently.**

- The "all branches" ruleset requires signed commits with no bypass. Renovate
  must be set to `platformCommit: "enabled"`, which creates commits through the
  GitHub API so GitHub signs them. Without it every Renovate pull request is
  refused, with no readable reason.
- This estate's own images are not Renovate's to bump. The build that makes a
  digest knows it; asking a bot to rediscover it is circular (see #375).
- Flux cannot read `scripts/versions.env` directly. It needs a ConfigMap
  generated from it and handed to `postBuild.substituteFrom`, which is the part
  that makes the file genuinely single rather than one of two.
- The Renovate workflow lives under `.github/workflows/`, so it arrives as a
  patch.

### 3. Back up workload data with Velero, and prove it with a round trip

**What they do.** Velero backs up selected volumes to an off-site bucket on a
schedule, with a bucket lifecycle rule as a cost bound. The backup bucket belongs
to a stack whose lifecycle is separate from any cluster's. And
`.taskfiles/dr/` holds an exercise that is worth more than the backup: seed a
sentinel with a known UUID and checksum, back up, **destroy the cluster,
rebuild it**, restore, and verify the checksum matches.

Their [`backup-strategy.md`](https://github.com/ionfury/homelab/blob/main/docs/architecture/backup-strategy.md)
also carries a data protection matrix - every stateful workload, its backup
mechanism, its recovery point objective and its retention - and a section of
"conscious trade-offs" for each thing deliberately not backed up. That document
is cheap and this estate should have one before it has a second stateful
workload.

**How it has to differ here.** They use CSI snapshots with Velero's data mover.
OpenEBS hostpath has no snapshot support, so this estate would use Velero's
**file-system backup** instead: the node agent reads the mounted volume with
Kopia and uploads it. R2 speaks the S3 API, so the target is the workloads
bucket that already exists and already survives a teardown.

Two details specific to the game server:

- A backup taken mid-save can catch a half-written world. Valheim 1.0 keeps
  several generations and writes `_main.N.ok` only when a save completes, so a
  restore can fall back a generation. A Velero pre-backup hook asking the server
  to save is better still.
- The server already writes its own `_backup_auto-*` directories to the same
  volume, which are free local rollback and unbounded growth at once (see the
  comment on #372). Retention on the volume is part of the job.

**What it closes here.** #372, with the ecosystem's tool rather than the bespoke
CronJob that issue originally imagined.

**Cost.** Velero is a new supplier. The round-trip exercise fits the existing e2e
tier, which already builds an estate from nothing and tears it down; the new
part is seeding a world sentinel before and checking it after.

### 4. Classify secrets by persistence and origin, and let the cluster fetch them

**What they do.** [`secret-management.md`](https://github.com/ionfury/homelab/blob/main/docs/architecture/secret-management.md)
sorts every secret on two axes - does it survive a cluster rebuild, and is it
generated or supplied from outside - and gives each combination one mechanism.
Generated and ephemeral: a controller fills it in. Supplied from outside: the
External Secrets Operator (ESO) reads it from a store. Generated and persistent:
OpenTofu generates it once and writes it to the store, and ESO reads it back.

**Why it fits.** That decision tree is the question #345 is trying to answer,
already answered. The third tier is what this estate already does for the state
database password, which break-ground generates and writes to 1Password.

The friction it removes was felt directly on 2026-09-11. Workload secrets reach
the cluster through OpenTofu today, so changing a game password meant a
`converge` **and** a manual `rollout restart`, and skipping either failed
silently. With ESO the change is made in the store and the Secret follows.

**How it has to differ here.**

- ESO reads 1Password today through its 1Password provider, and OpenBao later
  through its Vault-compatible provider, so adopting it does not wait on
  OpenBao.
- The credential ESO holds must not be able to read estate secrets. That means
  a separate vault for workload secrets, so a cluster compromise yields
  workload secrets and nothing else. It is the fail-closed version of the split
  #345 already proposes.
- They generate the game password with an in-cluster controller that
  regenerates it on rebuild. That is wrong for a password people have to be told.
  It belongs in the supplied-from-outside tier.

**Cost.** ESO is a new supplier, and a credential inside the cluster that can
read a vault is a new thing to scope carefully.

### 5. Restart when configuration changes

**What they do.** [Reloader](https://github.com/stakater/Reloader) watches the
ConfigMaps and Secrets a workload mounts and rolls it when they change.

**Why it fits.** A changed ConfigMap or Secret restarts nothing on its own, and
that bit this estate several times in one day: each world, name and password
change needed a `rollout restart` that nothing enforced, and forgetting it looked
exactly like success.

**How it has to differ here.** Half of it needs no new supplier. The game's
ConfigMap is reconciled by Flux and can be produced by a kustomize
`configMapGenerator`, whose hash suffix changes the Deployment when the content
changes. The Secret is written by OpenTofu and cannot be hashed that way, so it
needs Reloader or ESO - and if section 4 lands, the two arrive together.

## Adapt

### 6. Promote an artefact of the whole Flux tree

**What they do.** On merge, a workflow packages the whole `kubernetes/` directory
as an OCI artefact with `flux push artifact`, bumps a patch version, and tags it.
The live cluster follows it through an `OCIRepository` with a semver range.
Their stated reason is the problem #357 describes: "the previous git-branch
approach only protected version changes, not configuration changes."

Worth noting that their README still describes an integration cluster gating the
live one, while their promotion document describes deploying directly to live and
treating a post-deploy [canary-checker](https://canarychecker.io/) failure as a
rollback signal rather than a gate. Read as evidence for the "lower-tier
environment" entry in [`ideas.md`](../ideas.md): a team with separate hardware
built that gate and no longer runs it.

**Where it agrees with this estate.** `environments-and-promotion.md` already
says to promote the artefact rather than the source, and that the promotion pull
request should be generated. An immutable artefact of the Flux tree is exactly
the thing to promote, and it gates platform configuration as well as workloads,
which answers #357 without building a staging cluster that note argues against.

**Where it disagrees, and why ours should win.** They auto-tag on every merge
and go straight to production, so no human decides a release. This estate keeps
that decision human on purpose. The adaptation:

- build the artefact on every merge, addressed by digest;
- keep a production pin file naming the digest;
- have a generated pull request bump the pin, which the operator approves;
- let a tag mark what was promoted rather than cause it.

**A drift this surfaced.** The design note's own table says a `v*` tag "marks; it
does not deploy". What was built on 2026-09-10 deploys production from a tag.
The note and the build disagree, and the note is the one this estate argued its
way to. #358 is where that gets resolved.

This also composes with per-application versions (#358): a whole-tree artefact
suits configuration-only modules like the game server, and an application with
its own repository can still bring its own `OCIRepository`.

### 7. Choose network policy by namespace profile

**What they do.** A default-deny `CiliumClusterwideNetworkPolicy` applies to
every application namespace. A namespace selects one profile by label -
`isolated`, `internal`, `internal-egress`, `standard` - and gains access to shared
services through further labels. Onboarding an application is creating its
namespace with the right metadata. A labelled escape hatch disables enforcement
for one namespace during an incident, and there are runbooks for using it and for
verifying policy with Cilium's own tools.

**Why it fits.** The epoch record's position is that shared workers are the
default and most workloads are untrusted. A profile turns "untrusted, reaches the
internet, nothing inbound" into a label, rather than a policy written per
application that the next application copies.

**How it has to differ.**

- Their internet profile uses Cilium's `world` entity, which includes the house
  network. This estate's egress rule excludes the private ranges explicitly,
  which is stricter, and should be the base of the equivalent profile here.
- Profiles still need per-application additions. The game server needed a narrow
  LAN rule for PlayFab Party's direct path, and they layer an application policy
  on top of the profile in the same way.
- The runbook worth copying first is the verification one. Cilium's drop monitor
  found the game server's disconnect after six theories had not; that tool should
  be the first question asked of a network fault here, not the last.

### 8. A separate root for what outlives the cluster

**What they do.** A `global` stack owns the backup buckets, PKI and the
generated-and-persistent secrets. Cluster stacks can be destroyed without
touching it, because it is a different state entirely.

**Why it fits.** This estate keeps the workloads bucket in the cluster's own
state, forgets it before a destroy, and adopts it back on the next ignition. The
teardown code calls that "the exception to the rule". A second OpenTofu root with
its own encrypted state removes the exception, and gives the future OpenBao
storage and any bucket lifecycle rules somewhere to live that no demolish can
reach.

**Cost.** A second state to encrypt and back up, and an ordering decision: either
ignition applies it first, or ignition must not depend on it.

### 9. Probes that mean "serving"

**What they do.** Their image exposes a status endpoint, and the chart uses it for
startup, liveness and readiness probes, with a long startup budget for world
generation.

**Why it fits.** This estate's game server Deployment has no probes, so Kubernetes
calls it Ready as soon as the process starts. On 2026-09-11 `rollout status`
reported a successful rollout over a pod that had not changed. This image has no
status server, but readiness can still mean something: the server's UDP socket
appearing in `/proc/net/udp` is a check an exec probe can make with no new code
and no new supplier.

## Do not adopt

**The Steam backend behind a LAN LoadBalancer.** They expose UDP 2456-2457 on a
LAN address with inbound from `world`. This estate chose the crossplay relay to
avoid an inbound path, and the isolation reasoning depends on that. Keep it.

**A game image that installs the game at runtime.** Their image downloads the
server with SteamCMD when it starts. Adopting that would end the image rebuild on
every game patch that caused an "incompatible version" on 2026-09-11. It would
also mean several gigabytes of unpinned bytes fetched at every restart, from a
pod whose egress is the internet, with nothing reviewed between Valve and
production - the opposite of pin everything. Their own commit that day moved from
a floating tag to a pinned one after a release "rolled onto live unreviewed". The
better answer here is to keep baking the game in and automate the bump (#375),
so a patch is a build, a generated commit, and a tag.

**Automerge straight through to production.** Their automerge soak is worth
copying; letting it reach production without a human is not, for the reason in
section 6.

**The rest of the platform.** Istio ambient, Longhorn, Garage, Authelia and
Dragonfly are sized for three clusters and a rack of hardware. Longhorn has
already been tried and removed here.

**Their agent permissions.** Their implementation agent runs with permissions
bypassed and a hook guarding `kubectl`. This estate's boundary is structural
instead - the agent holds no infrastructure credential at all - which is stronger
than any guard, and should stay that way.

## Where they hit the same problems

Recorded because a problem two estates hit independently is probably a property
of the stack rather than of either estate.

- **Valheim 1.0 made a world a directory.** Their image applied a file mode to
  every entry under `worlds_local`, including the new world directory, which lost
  its search bit and stopped the server writing its saves. This estate's import
  went through the same change on the same day and got the directory right.
- **A game release reaching production unreviewed.** Their fix was pinning the
  image. This estate already pins by digest and paid for it in rebuilds instead,
  which is the trade section 2 and #375 are about.
- **Documentation that describes a past design.** Their README describes an
  integration gate and a `storage` stack; the code has a `global` stack and
  deploys straight to live. The same kind of drift appeared here between the
  promotion note and the build (section 6). Neither estate has anything that
  notices.
