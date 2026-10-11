# The reading of the declarations, against declarations written for the test:
# what is asserted here must hold whichever applications the estate has, and
# for an estate that has none.

run "every_declaration_is_read" {
  command = plan

  variables {
    directory = "./tests/fixtures/declared"
    core      = "./tests/fixtures/core/none.json"
  }

  assert {
    condition     = output.names == tolist(["alpha", "beta", "gamma"])
    error_message = "not every directory holding a declaration was read as an application"
  }
  assert {
    condition     = jsonencode(output.routes) == jsonencode({ "back-door" = 9, "front-door" = 7, "side-door" = 8 })
    error_message = "the routes are not every application's, each at the host number it declared"
  }
  assert {
    condition     = keys(output.secrets) == ["alpha.alpha-backup", "alpha.alpha-keys"] && output.secrets["alpha.alpha-keys"].application == "alpha" && output.secrets["alpha.alpha-keys"].name == "alpha-keys" && output.secrets["alpha.alpha-keys"].keys.user.vault == "user"
    error_message = "a declared Secret is not listed under its application with its keys' sources"
  }
}

run "two_applications_cannot_declare_one_route" {
  command = plan

  variables {
    directory = "./tests/fixtures/shared"
    core      = "./tests/fixtures/core/none.json"
  }

  expect_failures = [output.routes]
}

# What of the core is reached from away is a route like any application's,
# and the core is not an application for it.
run "the_cores_routes_are_read_beside_the_applications" {
  command = plan

  variables {
    directory = "./tests/fixtures/declared"
    core      = "./tests/fixtures/core/one.json"
  }

  assert {
    condition     = jsonencode(output.routes) == jsonencode({ "back-door" = 9, "front-door" = 7, "side-door" = 8, "the-yard" = 5 })
    error_message = "the core's route is not among the routes, at the host number it declared"
  }
  assert {
    condition     = output.names == tolist(["alpha", "beta", "gamma"])
    error_message = "the core was read as an application"
  }
}

run "the_core_cannot_declare_a_route_an_application_has" {
  command = plan

  variables {
    directory = "./tests/fixtures/declared"
    core      = "./tests/fixtures/core/taken.json"
  }

  expect_failures = [output.routes]
}

# The estate's own applications, whatever they are: the declarations parse, no
# two of them claim one route, and each directory names a namespace.
run "the_estates_own_declarations_are_read" {
  command = plan

  assert {
    condition     = alltrue([for name in output.names : can(regex("^[a-z]([a-z0-9-]*[a-z0-9])?$", name))])
    error_message = "an application's directory is its namespace, and one of them is not a name a namespace can have"
  }
}
