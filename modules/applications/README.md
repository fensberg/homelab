# Applications

What runs on the platform. One directory per application, holding everything
that is that application's and nothing that is anybody else's.

**The rule the layout exists for:** removing an application is deleting its
directory here and its block in each site that runs it. Nothing else in the
repository names an application, and
`TestAnApplicationIsRemovedByDeletingItsDirectoryAndItsBlocks` proves it by
doing exactly that to each one and running every guard. See "Removing an
application touches only its own directories" in
[`docs/epochs/02-abstraction.md`](../../docs/epochs/02-abstraction.md).

## What an application's directory holds

```text
<name>/
  application.json   what the application declares it needs from the estate
  pins.env           what its image is built from, beside the estate's pins
  image/             a Dockerfile the estate builds and publishes itself
  base/              Kubernetes manifests: the thing that runs
  <environment>/     its settings for one environment, built on ../base
  tests/             its own tests (a Go module) and mutations.yml, their proofs
```

The directory's name is the application's name, its namespace, and its item
in a site's vault.

The `image/` half exists because most self-hosted software has no official
container image, and the estate's rule is not to take an unvetted one. The
runner image set the pattern: a committed Dockerfile, versions fed from pins,
published to the organisation's own registry and pinned by digest wherever it
is used.

## What it declares

`application.json` is how the estate's general mechanisms learn about an
application without knowing it by name. Each mechanism reads every
application's declaration (`scripts/details/applications` for the programs,
`modules/infrastructure/applications` for OpenTofu); a field nothing reads is
refused.

| Field             | Says                                                                                               | Read by                                               |
| ----------------- | -------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `requires`        | The other applications this one cannot run without. The one place an application may name another  | The guard over each site's blocks                     |
| `routes`          | Services that need an address an enrolled device is routed to: name and host number                | The address plan, the estate's tunnel, the platform   |
| `secrets`         | The Secrets its namespace is given, and for each key where the value comes from                    | The platform module; the contractor, for vault fields |
| `release`         | How to read the version from the built image, with a line the software really prints as proof      | The fabricator                                        |
| `upstream`        | Where its supplier publishes builds, and which line of `pins.env` records the one it is built from | The expediter and its standing order                  |
| `before_teardown` | How to take a backup now, before the site it runs on is destroyed                                  | The contractor's teardown                             |

A secret key's value comes from exactly one of: `vault` (a field of the
application's item in the site's vault), `generated` (such a field the estate
creates when it is not there), `storage` (a fact about the bucket of the
environment the site runs it in) or `value` (written in the declaration,
because it is no secret but is read from the same Secret).

## What keeps an application to itself

Not a rule somebody has to remember. Each of these is how the thing is built:

- **Its release holds its own directory and nothing else.** The fabricator
  stages only `modules/applications/<name>/`, renders every environment from
  that copy and publishes it. A manifest that builds on a file outside the
  directory does not render there, so it is never released.
- **It is reconciled as its own identity, into its own namespace.** A site's
  block names the service account `application-<name>`, which the platform
  binds to the application's namespace and nothing else. A manifest reaching
  into another namespace, or for anything cluster-wide, is refused by the API
  server.
- **Its secrets are made from its own vault item.** A declaration can name a
  field, never an item, a bucket or a namespace.
- **What it requires, it declares.** A site given an application must run
  what it requires, which starts first.

What is not enforced by the cluster yet is the network: nothing stops an
application's own NetworkPolicy from permitting traffic to another's
namespace. The estate's address space is refused to every application by a
guard, and that is all.

## Where an application runs

On the shared workers, in a namespace of its own, with NetworkPolicy between
it and everything else. That is the default and it covers most things.

A dedicated zone - its own subnet, vnet and machine - is what a workload gets
when its **exposure** or its **blast radius** earns one, not because its code
came from outside. See
[`docs/epochs/03-workload.md`](../../docs/epochs/03-workload.md) for the
tiering and for why provenance is the wrong axis.

## Giving a site an application

Add its block to `clusters/<site>/applications.yaml`: the release source
named for it, and the Kustomization that reconciles one environment's
settings from it. The site's next converge creates the namespace, the Secrets
and the identity; until then the Kustomization waits. The vault item
`<site>/<name>` has to hold every field the application's secrets read, apart
from the generated ones.
