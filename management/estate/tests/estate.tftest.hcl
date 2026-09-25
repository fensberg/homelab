# The estate root, planned against a mocked Cloudflare: no account is reached
# and no credential is needed, so this runs wherever `tofu test` does.

mock_provider "cloudflare" {}

variables {
  config_path = "./tests/fixtures/valid.json"
}

run "valid_estate_plans_cleanly" {
  command = plan

  assert {
    condition     = length(cloudflare_zero_trust_access_policy.members.include[0].email) == 2
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
    condition = toset([for t in cloudflare_zero_trust_split_tunnel.estate.tunnels : t.address]) == toset([
      for addr in values(jsondecode(file("../tunnel-routes.json")).routes) : "${addr}/32"
    ])
    error_message = "the split tunnel does not carry exactly the routes in management/tunnel-routes.json"
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
