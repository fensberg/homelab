variable "directory" {
  type        = string
  default     = null
  description = <<-EOT
    Where the applications are. Left unset everywhere but this module's own
    tests, so that every caller reads the applications beside the module it
    was itself read from - a site reads those of the commit it is pinned to.
  EOT
}

variable "core" {
  type        = string
  default     = null
  description = <<-EOT
    Where the core says what of it is reached from away. Left unset everywhere
    but this module's own tests, for the reason the directory above is.
  EOT
}

locals {
  # What of the core is reached from away, beside what each application
  # declares of itself. The core is not an application and declares nothing
  # else here: it is read for its routes alone, so that the address plan is
  # handed every route a site has from one place. Written whole, like the
  # declarations below, because that is what puts it in a release.
  core_declaration = "${path.module}/../../../clusters/core/routes.json"
  core_routes      = jsondecode(file(coalesce(var.core, local.core_declaration))).routes

  # Every application's declaration, reached from the repository's top as
  # every file outside a module is. Written whole and in one string - the
  # directory, and which file of each application - because that string is
  # what puts the declarations in a release of the platform, and nothing
  # else of an application's (homelab/details/platform): a change to one is
  # a new version of the platform, and a change to an application's manifests
  # is not.
  declarations = "${path.module}/../../../modules/applications/*/application.json"
  directory    = coalesce(var.directory, dirname(dirname(local.declarations)))

  # Each, by the name of its directory.
  declared = {
    for f in fileset(local.directory, "*/${basename(local.declarations)}") :
    dirname(f) => jsondecode(file("${local.directory}/${f}"))
  }

  # Each route as its application declared it. Grouped by name rather than
  # merged, because a merge would let the second application to declare a
  # route silently take it from the first.
  route_claims = {
    for claim in concat(
      flatten([
        for app, d in local.declared : [
          for name, host in try(d.routes, {}) : { app = app, name = name, host = host }
        ]
      ]),
      [for name, host in local.core_routes : { app = "the core", name = name, host = host }],
    ) : claim.name => claim...
  }

  # Each declared Secret, by "<application>.<name>": which application's
  # namespace it goes in, and each key's source. A dot, which neither name
  # can hold, so the key reads back as the two and is one a `moved` block can
  # be written to.
  secrets = merge([
    for app, d in local.declared : {
      for name, keys in try(d.secrets, {}) : "${app}.${name}" => {
        application = app
        name        = name
        keys        = keys
      }
    }
  ]...)
}

output "names" {
  description = "Every application that declares itself."
  value       = sort(keys(local.declared))
}

output "routes" {
  description = "Every Service that needs an address known before it exists, an application's or the core's: name => host number in each site's service range. The address plan's fixed_addresses."
  value       = { for name, claims in local.route_claims : name => claims[0].host }

  precondition {
    condition     = alltrue([for claims in values(local.route_claims) : length(claims) == 1])
    error_message = "A route of one name is declared twice, by two applications or by one and the core, so two Services would be given one address: ${join(", ", [for name, claims in local.route_claims : name if length(claims) > 1])}."
  }
}

output "secrets" {
  description = "Every declared Secret, by \"<application>.<name>\": its application, its name, and each key's source."
  value       = local.secrets
}
