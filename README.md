# homelab

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
│ ├── Install-Dependencies.ps1 # One-time workstation setup
│ └── Start-Homelab.ps1 # Nine-phase ignition sequence
│
├── config/
│ ├── management.tpl.json # Secret contract, rendered by op inject
│ └── fleet.example.json # Shape of the fleet document (real one is in the vault)
│
├── management/ # THE IGNITION TIER (Local Execution)
│ ├── hypervisor/
│ │ └── hypervisor-prep.yml # Ansible: repos, overlay net, RBAC, SDN (inventory is generated)
│ └── cluster/
│ ├── backend_pg.tf.disabled # Renamed in at state-migration time
│ ├── compute.tf # Talos control-plane VMs
│ ├── talos.tf # Machine config, bootstrap, kubeconfig
│ ├── database.tf # Namespace and secrets for the state database
│ ├── overlay-network.tf # Tagged auth key for this site
│ ├── object-storage.tf # Backup bucket
│ ├── gitops.tf # Flux bootstrap
│ └── versions.tf # Provider definitions
│
├── clusters/management/ # Reconciled by Flux, not applied locally
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
│ └── applications/
│ └── fo0-app/
│ ├── deployment.yaml # Kubernetes: App pods
│ └── kustomization.yaml # Base Kustomize configuration
│
└── environments/ # THE WORKLOAD TIER (Pointer Configs)
├── staging/
│ ├── infrastructure/
│ │ └── foo.tf
│ └── applications/
│ └── kustomization.yaml # Flux/Kustomize overlay for Staging
└── production/
├── infrastructure/
│ └── foo.tf
└── applications/
└── kustomization.yaml # Flux/Kustomize overlay for Production
