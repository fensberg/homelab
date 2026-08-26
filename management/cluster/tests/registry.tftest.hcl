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
