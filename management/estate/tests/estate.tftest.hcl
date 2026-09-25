# The estate root, planned against a mocked Cloudflare: no account is reached
# and no credential is needed, so this runs wherever `tofu test` does.

mock_provider "cloudflare" {
  mock_data "cloudflare_account_api_token_permission_groups_list" {
    defaults = {
      result = [{ id = "fixture-r2-bucket-write", name = "Workers R2 Storage Bucket Item Write", scopes = ["com.cloudflare.edge.r2.bucket"] }]
    }
  }
}

variables {
  config_path = "./tests/fixtures/valid.json"
}

run "valid_estate_plans_cleanly" {
  command = plan

  assert {
    condition     = length(cloudflare_zero_trust_access_policy.members.include) == 2
    error_message = "the members list should be split on commas and trimmed into one email each"
  }
}

# Every route a site sends through its tunnel must also be one an enrolled
# device sends to Cloudflare, or the site's route leads nowhere from a device.
# Both read management/tunnel-routes.json; this proves the estate reads all of
# it.
run "the_split_tunnel_carries_every_route" {
  command = plan

  assert {
    condition = toset([for t in cloudflare_zero_trust_device_default_profile.estate.include : t.address]) == toset([
      for addr in values(jsondecode(file("../tunnel-routes.json")).routes) : "${addr}/32"
    ])
    error_message = "the split tunnel does not carry exactly the routes in management/tunnel-routes.json"
  }
}

# What a site is granted, end to end: named <estate>-<site>-<purpose> from the
# vault's names made slugs, with a key per bucket whose secret is the SHA-256
# of the token - which is how R2 reads an API token as an S3 key pair.
run "a_site_is_granted_its_buckets_by_name_and_a_key_for_each" {
  command = apply

  assert {
    condition = alltrue([
      for p in ["database", "state", "staging", "production"] :
      output.grants["site0"].object_storage["${p}_bucket"] == "example-north-street-office-${p}"
    ])
    error_message = "the site's buckets are not named <estate>-<site>-<purpose> from the vault's names as slugs"
  }

  assert {
    condition = alltrue([
      for p in ["database", "state", "staging", "production"] :
      length(output.grants["site0"].object_storage["${p}_secret_access_key"]) == 64
    ])
    error_message = "a bucket's secret access key is not a SHA-256 of its token, which is the only form R2 accepts"
  }

  assert {
    condition     = output.grants["site0"].identity.name == "North Street Office" && output.grants["site0"].tunnel.provider == "cloudflare"
    error_message = "the site is not granted its name and its tunnel"
  }
}

# An enrollment policy that admits nobody is refused rather than converged -
# or, worse, read by the vendor as admitting anyone.
run "no_members_fails_the_members_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/no-members.json"
  }

  expect_failures = [cloudflare_zero_trust_access_policy.members]
}

# R2 refuses a name over 63 characters. Refused at plan, before any bucket or
# token exists, rather than part way through an apply.
run "a_bucket_name_too_long_for_r2_fails_before_anything_is_created" {
  command = plan

  variables {
    config_path = "./tests/fixtures/long-site-name.json"
  }

  expect_failures = [output.grants]
}

run "a_site_name_with_nothing_to_name_a_bucket_by_fails" {
  command = plan

  variables {
    config_path = "./tests/fixtures/unnameable-site.json"
  }

  expect_failures = [output.grants]
}
