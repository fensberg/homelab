# Application modules

The reusable half of a workload: its image, and the Kubernetes base an
environment overlays. An environment under `environments/` points at what is
here and supplies the values; nothing here knows which environment it is in.

## What a module holds

```text
<name>/
  image/     a Dockerfile this estate builds and publishes itself
  base/      Kubernetes manifests, environment-agnostic
```

The `image/` half exists because most self-hosted software has no official
container image, and the estate's rule is not to take an unvetted one. The
runner image set the pattern: a committed Dockerfile, versions fed from
`scripts/versions.env`, published to the organisation's own registry and pinned
by digest wherever it is used.

## Where a module runs

On the shared workers, in a namespace of its own, with NetworkPolicy between
it and everything else. That is the default and it covers most things.

A dedicated zone - its own subnet, vnet and machine - is what a workload gets
when its **exposure** or its **blast radius** earns one, not because its code
came from outside. See
[`docs/epochs/03-workload.md`](../../docs/epochs/03-workload.md) for the
tiering and for why provenance is the wrong axis.
