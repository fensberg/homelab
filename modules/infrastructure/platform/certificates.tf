# =============================================================================
# Certificates. Tool: cert-manager. Vendor of the domain's records: Cloudflare.
#
# Everything a device reaches is served over HTTPS, under a name a person
# types, with a certificate any browser trusts. cert-manager gets one for
# each name and renews it, and to be issued one it proves the name is the
# estate's by a record under the estate's domain.
#
# Three owners, as the tunnel has. The estate makes the credential that can
# change the domain's records and grants it to this site
# (management/estate/site/served.tf); this root creates the namespace and the
# Secret the credential is read from; cert-manager itself is declared in git
# under clusters/core/ and reconciled by Flux.
#
# WHAT THE CREDENTIAL REACHES. Every record under the estate's domain, and
# not only the ones that prove a name: the vendor scopes one to a domain and
# no narrower. It is in one Secret, in a namespace that holds cert-manager
# and nothing else, and it is given to nothing that routes or serves. The
# front door in particular holds no credential that could get a certificate
# (docs/epochs/04-observability.md).
# =============================================================================
resource "kubernetes_namespace" "certificates" {
  metadata {
    name = "cert-manager"
  }
}

# Named for the one value it holds.
resource "kubernetes_secret" "dns_token" {
  metadata {
    name      = "dns-token"
    namespace = kubernetes_namespace.certificates.metadata[0].name
  }
  data = {
    token = local.certificates.dns_token
  }
}
