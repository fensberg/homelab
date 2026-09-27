variable "sites" {
  type        = any
  description = <<-EOT
    The config's sites map, whole. Every site is planned together, because the
    property that matters most - no two sites ever share a range - is a fact
    about the estate, not about one site.

    Not validated here. Whether a config is acceptable is decided at the
    edges - registry.tf and scripts/contractor/config, held to the same
    verdicts by tests/fixtures/manifest.json - and this module only computes
    from a config they have accepted. A unique octet from 1 to 95 is what
    guarantees no two ranges overlap.
  EOT
}

variable "domain" {
  type        = string
  default     = ""
  description = <<-EOT
    The estate's domain, from the vault. Empty until it is declared, and every
    name that needs it is omitted until then rather than invented.
  EOT
}

variable "aliases" {
  type        = map(string)
  default     = {}
  description = <<-EOT
    Short names that point at one site's service: "proxmox" = "site0" makes
    proxmox.<domain> the same address as proxmox.site0.<domain>. One line per
    alias, so moving one to another site is one edit.
  EOT

  validation {
    condition     = alltrue([for site in values(var.aliases) : contains(keys(var.sites), site)])
    error_message = "An alias points at a site the config does not declare."
  }
}
