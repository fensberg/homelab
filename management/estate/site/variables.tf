variable "account_id" {
  type        = string
  description = "The Cloudflare account: the estate."
}

variable "estate" {
  type        = string
  description = "The estate's name as a slug, which names the site's tunnel beside its key."
}

variable "prefix" {
  type        = string
  description = "What every bucket name starts with: <estate>-<site name>, as slugs. Worked out once, at the estate, where its length is checked."
}

variable "site" {
  type        = string
  description = "The site's key, as in git: site0."
}

variable "name" {
  type        = string
  description = "The site's name, from the estate vault. It names the site's buckets and reaches no plan address."
}

variable "tunnel_routes" {
  type        = map(string)
  description = "What an enrolled device may reach through this site's tunnel, by name."
}

variable "r2_bucket_write" {
  type        = string
  description = "The id of Cloudflare's permission group for reading and writing objects in one bucket."
}

variable "r2_bucket_read" {
  type        = string
  description = "The id of Cloudflare's permission group for listing and reading objects in one bucket, and changing none."
}

variable "served" {
  type = map(object({
    hostname = string
    at       = string
    address  = string
  }))
  description = "What this site serves by name: the name a person types, and the address it is reached at."
}

variable "rules_from" {
  type        = number
  description = "Where in the vendor's order of rules this site's begin. Ahead of every rule the estate did not make."
}

variable "zone" {
  type        = string
  description = "The vendor's id for the estate's domain, which a site's certificates are proved under."
}

variable "zone_read" {
  type        = string
  description = "The id of the vendor's permission group for reading a domain."
}

variable "dns_write" {
  type        = string
  description = "The id of the vendor's permission group for changing a domain's records."
}
