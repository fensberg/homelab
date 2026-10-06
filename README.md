# homelab

```text
homelab/
├── .env # Global variables for local task execution
├── .gitignore # Prevents rendered secrets/state from leaking
├── CLAUDE.md # Project invariants and conventions
├── taskfile.yml # Thin wrappers over the start button
│
├── .github/
│ └── workflows/
│ ├── pr-validation.yml # Actions: secret, lint, SAST, IaC and posture scans
│ └── deploy-infrastructure.yml # Actions: applies staging (branch) / production (tag)
│
├── docs/epochs/ # Why things are the way they are, per phase
│
├── scripts/ # THE START BUTTON
│ ├── install-dependencies.sh # One-time workstation setup
│ └── contractor/ # Nine-phase ignition sequence (Go)
│
├── config/
│ └── management.tpl.json # The one config: sites[], topology, secret references
│
├── management/ # THE IGNITION TIER (Local Execution)
│ ├── hypervisor/
│ │ └── hypervisor-prep.yml # Ansible: repos, overlay net, RBAC, SDN (inventory is generated)
│ └── cluster/
│ ├── backend_pg.tf.disabled # Renamed in at state-migration time
│ ├── registry.tf # Plan-time invariants: index range, vendor lock
│ ├── compute.tf # Talos control-plane VMs
│ ├── talos.tf # Machine config, bootstrap, kubeconfig
│ ├── database.tf # Namespace and secrets for the state database
│ ├── overlay-network.tf # Tagged auth key for this site
│ ├── gitops.tf # Flux bootstrap
│ └── versions.tf # Provider definitions
│
├── clusters/core/ # Reconciled by Flux, not applied locally
│ ├── infra-controllers.yaml # Layer 1: operators (installs CRDs)
│ ├── infra-configs.yaml # Layer 2: resources using those CRDs
│ └── infrastructure/
│ ├── controllers/ # CloudNativePG operator
│ └── configs/ # The state database itself
│
├── modules/ # THE ABSTRACTION TIER (Write Code Once)
│ ├── infrastructure/
│ │ └── foo-inf/
│ │ ├── main.tf
│ │ └── variables.tf
│ └── applications/ # THE WORKLOAD TIER (one directory per application)
│ └── foo-app/
│ ├── application.json # What it declares it needs: secrets, routes, release
│ ├── image/ # Its Dockerfile, built and published by the estate
│ ├── base/ # Kubernetes: the thing that runs
│ ├── staging/ # Its settings for one environment, built on the base
│ ├── production/
│ └── tests/ # Its own tests, and the proofs they catch anything
│
└── clusters/<site>/applications.yaml # One block per application a site runs
```
