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
