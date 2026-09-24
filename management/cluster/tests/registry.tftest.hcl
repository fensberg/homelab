# The HCL half of the config-contract corpus.
#
# Every run block below has a twin: a case of the same name in
# tests/fixtures/manifest.json, which drives the Go half of the same corpus
# through config.ResolveSiteNetwork. Both implementations gate a real
# deployment, so both have to reach the same verdict on the same input -
# scripts/contractor/internal/config/contract_test.go is what proves they do, and
# it fails if a case exists on one side and not the other.
#
# Adding a case: write the fixture, add it to manifest.json, add a run block
# here with the identical name.

variables {
  site        = "site0"
  config_path = "./tests/fixtures/valid.json"
}

run "valid_config_plans_cleanly" {
  command = plan

  # terraform_data.invariants needs no provider - it's the built-in
  # terraform.io/builtin/terraform resource. Targeting just it means this
  # test only ever evaluates registry.tf's own locals/preconditions, never
  # compute.tf's real proxmox/talos/kubernetes/tailscale/cloudflare
  # resources, which would need real providers (or extensive mocking) to
  # plan at all.
  plan_options {
    target = [terraform_data.invariants]
  }
}

# The odd one out: this is a variable validation, not a resource
# precondition, so expect_failures names var.site rather than the resource.
# See variables.tf for why it had to move there to fire at all.
run "unknown_site_fails_its_precondition" {
  command = plan

  variables {
    site = "not-a-site"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [var.site]
}

run "vendor_mismatch_fails_the_vault_attestation_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/vendor-mismatch.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

# Config and vault agree with each other, on a vendor this root does not
# implement. A check that only compared those two would pass this.
run "unimplemented_vendor_fails_even_when_config_and_vault_agree" {
  command = plan

  variables {
    config_path = "./tests/fixtures/unimplemented-vendor.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "tunnel_vendor_mismatch_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/tunnel-vendor-mismatch.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "missing_vault_provider_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/missing-vault-provider.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "octet_out_of_range_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/octet-out-of-range.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "duplicate_octet_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/duplicate-octet.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "no_hypervisor_nodes_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/no-hypervisor-nodes.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "control_plane_count_below_one_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/control-plane-count-zero.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "aws_shaped_access_key_fails_on_a_cloudflare_site" {
  command = plan

  variables {
    config_path = "./tests/fixtures/aws-shaped-key.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "negative_worker_count_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/negative-worker-count.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

run "negative_zone_node_count_fails_its_precondition" {
  command = plan

  variables {
    config_path = "./tests/fixtures/negative-zone-node-count.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  expect_failures = [terraform_data.invariants]
}

# Unprivileged user namespaces reach only the zone that declares them.
#
# Talos turns them off on every machine, and that hardening is worth keeping
# everywhere but the one machine a rootless builder needs it on. A patch that
# keyed off "any zone" rather than the zone's own declaration would hand it to
# every untrusted machine - the least trusted hardware on the estate, losing
# the hardening first.
#
# Both zones also carry their label, which is how a tenant is required onto
# its zone rather than merely tolerated there.
run "zone_user_namespaces_reach_only_the_zone_that_declares_them" {
  command = plan

  variables {
    config_path = "./tests/fixtures/zone-user-namespaces.json"
  }

  plan_options {
    target = [terraform_data.invariants]
  }

  assert {
    condition     = strcontains(join("\n", local.dmz_by_zone["redacted"]), "max_user_namespaces")
    error_message = "the zone declaring user_namespaces does not get the sysctl, so a rootless builder there cannot start"
  }

  assert {
    condition     = !strcontains(join("\n", local.dmz_by_zone["example"]), "max_user_namespaces")
    error_message = "a zone that did not declare user_namespaces gets them anyway, losing Talos's hardening on a machine that never asked"
  }

  assert {
    condition     = alltrue([for k in ["redacted", "example"] : strcontains(join("\n", local.dmz_by_zone[k]), "node-labels")])
    error_message = "a zone machine carries no untrusted-zone label, so its tenant can tolerate the zone but cannot be required onto it"
  }
}
