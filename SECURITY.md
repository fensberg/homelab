# Security Policy

This repository provisions and manages real infrastructure - a finding here
can matter well beyond the code itself, so please report it privately rather
than opening a public issue.

## Reporting a Vulnerability

Use GitHub's private vulnerability reporting for this repository: open the
**Security** tab -> **Advisories** -> **Report a vulnerability**. That opens a
private conversation with the maintainer that nobody else can see until it's
resolved.

Do not open a public issue or pull request for a security finding.

## Scope

This is a personal homelab project (Proxmox -> Talos -> Flux, provisioned
with OpenTofu and Ansible; see [`CLAUDE.md`](CLAUDE.md) for the full
architecture). In scope:

- Anything that could cause a secret to land in git
- A misconfiguration in `.github/workflows/` that weakens the controls
  `CLAUDE.md` describes (egress policy, token permissions, required checks)
- A vulnerability in the `scripts/steward` Go program itself

Out of scope: vulnerabilities in third-party dependencies this project only
consumes (Proxmox, Talos, Flux, Terraform providers, etc.) - please report
those upstream instead.

## Response

This is maintained by one person outside of full-time work, so there's no
guaranteed SLA, but a private report will get a response.
