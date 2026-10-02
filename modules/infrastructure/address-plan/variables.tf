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

variable "fixed_addresses" {
  type        = map(number)
  default     = {}
  description = <<-EOT
    Services that need an address known before they exist, because something
    outside the cluster routes to it: name => host number in each site's
    service range. Each application declares its own. Every site gets
    the same host number in its own range, so a route is one line however
    many sites there are, and no two sites ever share the address.
  EOT

  # Kubernetes keeps the bottom of a service range for addresses chosen by
  # hand and allocates from the rest: min(max(16, size/16), 256) addresses,
  # which is 64 for a site's /22. Inside that band an address cannot already
  # have been handed to some other Service.
  validation {
    condition     = alltrue([for n in values(var.fixed_addresses) : n >= 1 && n <= 63 && floor(n) == n])
    error_message = "A fixed address must be a whole host number from 1 to 63: the band of a site's service range that Kubernetes never allocates by itself."
  }
  validation {
    condition     = length(distinct(values(var.fixed_addresses))) == length(var.fixed_addresses)
    error_message = "Two fixed addresses share a host number, so two Services would claim one address."
  }
}
