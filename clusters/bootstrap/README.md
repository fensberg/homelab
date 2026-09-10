# Bootstrap manifests

Applied by OpenTofu while the cluster is coming up, **before Flux exists and
before any node is Ready**. Flux does not reconcile anything here.

That last part is the reason this directory is a sibling of `management/`
rather than a directory inside it. `clusters/management/` is the path Flux
syncs, with `prune: true` — anything placed there acquires a second owner, and
a component with two owners is one this repository has no way to reason about.

## Why anything is here at all

The CNI. A node with no CNI never reaches `Ready`, and Flux cannot schedule
until nodes are `Ready`, so the pod network is the one component that cannot
arrive the way OpenEBS, CloudNativePG and the runner do. It has to be installed
by whatever built the cluster.

`cilium.yaml` is generated — run `task render-cni` after changing
`cilium-values.yaml` or `CILIUM_VERSION` in `scripts/versions.env`, and commit
both. It is committed rather than fetched at apply time so its image digests
are reviewable here and `tests/go/repo/suppliers_test.go` can read them; that
guard walks all of `clusters/`, which is the other reason this lives under this
tree.

The full reasoning, including why Talos `inlineManifests` and the Helm provider
were both rejected, is in
[`docs/epochs/03-workload.md`](../../docs/epochs/03-workload.md).
