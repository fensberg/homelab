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
  sites_path  = "./tests/fixtures/sites.json"
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
# Both come from what the applications declare, through the address plan;
# this proves the estate carries every route of every site, each at the
# site's own address. Against applications written for the test, so that it
# holds whichever the estate has: two of them, with three routes between them.
run "the_split_tunnel_carries_every_route" {
  command = plan

  variables {
    config_path       = "./tests/fixtures/two-plots.json"
    applications_path = "../../modules/infrastructure/applications/tests/fixtures/declared"
  }

  assert {
    condition = toset([for t in cloudflare_zero_trust_device_default_profile.estate.include : t.address]) == toset([
      "10.196.40.7/32", "10.196.40.8/32", "10.196.40.9/32",
      "10.196.80.7/32", "10.196.80.8/32", "10.196.80.9/32",
    ])
    error_message = "the split tunnel does not carry every route the applications declare at each site's own address"
  }
}

# A plot the sites do not declare would get a tunnel that routes nothing.
run "a_plot_with_no_site_is_refused" {
  command = plan

  variables {
    sites_path = "./tests/fixtures/no-sites.json"
  }

  expect_failures = [output.grants]
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
    condition = alltrue([
      for p in ["database", "state", "staging", "production"] :
      length(output.grants["site0"].object_storage["${p}_reader_secret_access_key"]) == 64 &&
      output.grants["site0"].object_storage["${p}_reader_access_key_id"] != ""
    ])
    error_message = "a bucket has no read-only key of its own for the safety officer"
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

# The workstation has a tunnel of its own with one route - itself - and an
# enrolled device sends that address to Cloudflare beside every site's. Its
# token is granted to nobody: the workstation pulls it when it installs.
run "the_workstation_has_a_tunnel_of_its_own_with_one_route" {
  command = plan

  assert {
    condition     = [for m in module.workstation : m.route] == ["192.0.2.50/32"]
    error_message = "the workstation's tunnel does not route the workstation's own address, and only that"
  }

  assert {
    condition     = contains([for t in cloudflare_zero_trust_device_default_profile.estate.include : t.address], "192.0.2.50/32")
    error_message = "an enrolled device does not send the workstation's address to Cloudflare, so the route leads nowhere from a device"
  }

  assert {
    condition     = [for m in module.workstation : m.name] == ["example-workstation"]
    error_message = "the workstation's tunnel is not named for the estate and the workstation"
  }

  assert {
    condition     = !contains(keys(output.grants), "workstation") && output.workstation_route == ["192.0.2.50/32"]
    error_message = "the workstation's tunnel token is granted into a vault, or its route is not said"
  }
}

# An estate that gives the workstation no address has no such tunnel.
run "an_estate_with_no_workstation_has_no_tunnel_for_one" {
  command = plan

  variables {
    config_path = "./tests/fixtures/two-plots.json"
  }

  assert {
    condition     = length(module.workstation) == 0 && output.workstation_route == []
    error_message = "a tunnel was made for a workstation the estate does not have"
  }
}

# What the estate's config holds for the workstation has to be one address.
run "a_workstation_address_that_is_not_one_is_refused" {
  command = plan

  variables {
    config_path = "./tests/fixtures/workstation-not-an-address.json"
  }

  expect_failures = [output.workstation_route]
}

# The watchman is what says a site has gone quiet, and a site is granted what
# it rings with. The watchman is given the SHA-256 of each site's secret and
# never the secret, so nothing read out of its settings can ring for a site.
run "a_site_is_granted_what_it_rings_the_watchman_with" {
  command = apply

  variables {
    config_path = "./tests/fixtures/two-plots.json"
  }

  assert {
    condition = alltrue([
      for k, g in output.grants :
      g.heartbeat.url == "https://watchman.example.workers.dev/${k}" && length(g.heartbeat.secret) == 48
    ])
    error_message = "a site is not granted the watchman's address for it, at the account's own address for Workers, and a secret to ring with"
  }

  assert {
    condition     = length(distinct([for g in output.grants : g.heartbeat.secret])) == length(output.grants)
    error_message = "two sites ring with the same secret, so one could ring for the other after it has stopped"
  }

  assert {
    condition     = jsondecode(module.watchman.given_in_the_clear.SITES) == { for k, g in output.grants : k => sha256(g.heartbeat.secret) }
    error_message = "the watchman is not given exactly the SHA-256 of each site's secret, by the site's key"
  }

  assert {
    condition = alltrue([
      for k, g in output.grants : !strcontains(jsonencode(module.watchman.given_in_the_clear), g.heartbeat.secret)
    ])
    error_message = "the watchman is given a site's secret in the clear, which is what reading its settings would yield"
  }
}

# The operator's bound on the watchman, which is tighter than the plan's: no
# more than 200 writes a day for a site, and no silence said before two rings
# have been missed.
run "the_watchman_rings_no_more_often_than_the_operator_allows" {
  command = plan

  assert {
    condition     = module.watchman.use.writes_a_day / length(local.sites) <= 200
    error_message = "a site costs the watchman more than 200 writes a day, which is the operator's bound"
  }

  assert {
    condition     = module.watchman.timings.quiet_after > 2 * module.watchman.timings.looks_every
    error_message = "a silence is said before two rings have been missed, so one late ring is called a site gone quiet"
  }
}

# Everything the estate runs on the free plan is added up and held to it, and
# what the estate uses today fits.
run "what_the_estate_uses_fits_the_free_plan" {
  command = plan

  assert {
    condition     = output.free_plan_use.timers == 1 && output.free_plan_use.writes_a_day == 154 && output.free_plan_use.requests_a_day == 144
    error_message = "one site's watchman is not counted as one timer, 144 requests and 154 writes a day"
  }
}

# A plan that would go over is refused before anything changes. Planned here
# against a smaller plan than the real one: two sites, and room for one.
run "an_estate_that_would_go_over_the_free_plan_is_refused" {
  command = plan

  variables {
    config_path = "./tests/fixtures/two-plots.json"
    free_plan   = { timers = 5, requests_a_day = 100000, reads_a_day = 100000, writes_a_day = 300 }
  }

  expect_failures = [output.free_plan_use]
}

# The same for the timers, which are counted for the account and not by use.
run "a_timer_too_many_for_the_free_plan_is_refused" {
  command = plan

  variables {
    free_plan = { timers = 0, requests_a_day = 100000, reads_a_day = 100000, writes_a_day = 1000 }
  }

  expect_failures = [output.free_plan_use]
}

# The account's name under workers.dev is the name alone. Written as the
# address it makes, every site would be granted an address that is nowhere.
run "an_accounts_name_written_as_an_address_is_refused" {
  command = plan

  variables {
    config_path = "./tests/fixtures/workers-name-is-an-address.json"
  }

  expect_failures = [output.grants]
}
