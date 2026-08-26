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
