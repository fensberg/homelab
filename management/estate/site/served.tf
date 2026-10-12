# What this site serves by name: a rule that answers an enrolled device with
# where each name is reached, and the credential its certificates are got
# with. Why each is as it is, is in ../served.tf.

locals {
  # The names reached at one address, by that address's name. One rule for
  # each address and not for each name, since a rule answers with one
  # address and every name the site serves over the web is reached at one.
  names_at = {
    for at in distinct([for s in values(var.served) : s.at]) : at => {
      address   = one(distinct([for s in values(var.served) : s.address if s.at == at]))
      hostnames = sort([for s in values(var.served) : s.hostname if s.at == at])
    }
  }
  rule_order = { for i, at in sort(keys(local.names_at)) : at => var.rules_from + i }
}

resource "cloudflare_zero_trust_gateway_policy" "served" {
  for_each = local.names_at

  account_id  = var.account_id
  name        = "${var.name} names at ${replace(each.key, "-", " ")}"
  description = "Answers an enrolled device with where these names are reached. Declared by the estate."
  enabled     = true
  precedence  = local.rule_order[each.key]

  filters = ["dns"]
  traffic = "dns.fqdn in {${join(" ", [for h in each.value.hostnames : "\"${h}\""])}}"
  action  = "override"
  rule_settings = {
    override_ips = [each.value.address]
  }
}

# None, for a site that serves nothing by name: a credential that changes a
# domain's records is not made in case it is wanted.
resource "cloudflare_account_token" "certificates" {
  count      = length(var.served) > 0 ? 1 : 0
  account_id = var.account_id
  name       = "${var.site} certificates"

  policies = [{
    effect            = "allow"
    permission_groups = [{ id = var.zone_read }, { id = var.dns_write }]
    resources = jsonencode({
      "com.cloudflare.api.account.zone.${var.zone}" = "*"
    })
  }]
}
