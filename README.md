# homelab

homelab/
├── .env # Global variables for local task execution
├── .gitignore # Prevents rendered secrets/state from leaking
├── Taskfile.yml # The "Start Button" automation wrapper
│
├── .github/
│ └── workflows/
│ ├── deploy-staging.yml # Actions: Applies staging infrastructure (branch-tracked)
│ └── deploy-production.yml # Actions: Applies production infrastructure (tag-tracked)
│
├── config/
│ └── management.tpl.json # 1Password template for the Management Cluster
│
├── management/ # THE IGNITION TIER (Local Execution)
│ ├── hypervisor/
│ │ ├── inventory.tpl.yml # 1Password template for Ansible hosts
│ │ └── hypervisor-prep.yml # Ansible: Proxmox SDN, Tailscale, RBAC
│ └── cluster/
│ ├── backend_pg.tf.disabled # Renamed so Tofu ignores it initially
│ ├── main.tf # OpenTofu compute definitions
│ ├── versions.tf # Provider definitions
│ └── deploy-local.sh # Local -> Postgres migration script
│
├── modules/ # THE ABSTRACTION TIER (Write Code Once)
│ ├── infrastructure/
│ │ └── foo-inf/
│ │ ├── main.tf
│ │ └── variables.tf
│ └── applications/
│ └── foo-app/
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
