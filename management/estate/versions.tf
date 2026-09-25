# The estate: what every site and every node stands on.
#
# Built once, by `lawyer build-estate`, before any site - and never reached by
# a site's build. The lawyer holds the estate's credentials and only those; the
# contractor holds a site's and only those, so neither program can change what
# the other owns. A site demolish cannot touch anything here, because none
# of it is in a site's state: that is the whole point of a root of its own
# rather than a list of things a site teardown must remember to spare. The
# boundary between estate, site and node is in docs/epochs/02-abstraction.md.
#
# Only what belongs to the Cloudflare account as a whole lives here. A site's
# own tunnel, its routes and its buckets are the site's, even though they are
# made in the same account.
terraform {
  required_version = ">= 1.10.0"

  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 5.25" }
  }

  # The estate's own bucket, not any site's database: the estate cannot
  # depend on a site. The lawyer supplies the bucket, the account's S3
  # endpoint and the bucket's own credential at init (-backend-config and the
  # AWS_* environment), so nothing in git names the account. State is
  # encrypted by TF_ENCRYPTION exactly as a site's is, so the bucket holds
  # ciphertext.
  #
  # use_lockfile is the S3 backend's native lock: a second run fails to take
  # it rather than both writing. It needs the store to honour conditional
  # writes; a store that did not would fail the lock loudly, not skip it.
  backend "s3" {
    key                         = "estate.tfstate"
    region                      = "auto"
    use_lockfile                = true
    use_path_style              = true
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
  }
}

# The estate's own token, and the estate's master key: Access applications and
# policies, the account's device settings, R2, tunnels, and the account API
# tokens it mints for each site. That last permission is why it lives in the
# estate vault alone - it can make any narrower credential, so the narrower
# credentials are what sites are given instead.
provider "cloudflare" {
  api_token = local.access.api_token
}
