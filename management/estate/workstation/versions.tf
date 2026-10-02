# One site's plot: what the estate creates for a site and grants it.
#
# Created by the estate because only an account-wide credential can create it,
# and the point of the estate/site split is that no site holds one. The site is
# handed credentials narrowed to what is here - a key per bucket that reaches
# that bucket alone, and a tunnel's run token that serves that tunnel alone -
# so a site compromised is a site's plot compromised, never a sibling's.
#
# Instantiated once per site key from sites.tf. Addresses read
# module.site["site0"]...: the key is positional and in git, never the site's
# name, because a plan prints addresses.
terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare" }
  }
}
