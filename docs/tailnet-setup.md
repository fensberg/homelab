# One-time tailnet setup

Do this **once per tailnet**, before the first deployment into it. It is not
part of ignition, and deliberately so: the policy below belongs to the tailnet,
not to any one site. If every deployment managed it, each site would overwrite
the policy that every other site depends on.

Whose tailnet this is depends on the engagement:

- **Your own tailnet** for labs, demos and anything you keep. Set this up once
  and never again.
- **The client's tailnet** for work they own and keep after you leave. Ask for
  access before the visit, the same way you would ask for VPN credentials.

## 1. Policy file

In the Tailscale admin console, open **Access controls** and merge these two
blocks into the policy. Do not replace the whole file unless you know what the
existing rules do.

```jsonc
{
  // Who may apply the router tag. Narrow this from autogroup:admin if the
  // tailnet has operators who should not be able to route subnets.
  "tagOwners": {
    "tag:homelab-router": ["autogroup:admin"],
  },

  // Routes advertised by a node carrying the router tag are approved with no
  // human in the loop. This is what stops ignition silently hanging on an
  // unapproved subnet route.
  //
  // Each site advertises its whole /16 (see sites[] in the config), so covering
  // 10.0.0.0/8 here means adding a site never requires editing this policy
  // again. The tag is what constrains this, not the range: only nodes you
  // have tagged as routers can advertise anything at all.
  "autoApprovers": {
    "routes": {
      "10.0.0.0/8": ["tag:homelab-router"],
    },
  },
}
```

If you would rather be precise than convenient, list each site's /16
individually (`10.10.0.0/16`, `10.20.0.0/16`, ...) instead. That costs a policy
edit per new site and buys a smaller blast radius if a router is compromised.

> **Subnet collisions.** Two sites on one tailnet advertising overlapping
> ranges collide, and traffic goes to whichever route the tailnet resolves
> first. The site index prevents that: the octet is 10 + index, and two
> entries in sites[] cannot share an index.

## 2. OAuth client

**Settings → OAuth clients → Generate.**

| Setting | Value |
| --- | --- |
| Scope | write access to auth keys |
| Tags | `tag:homelab-router` |

The tag must already exist in `tagOwners` from step 1, or it cannot be
selected here. That ordering is why the policy comes first.

An OAuth client is used rather than a long-lived auth key because auth keys
expire after 90 days at most. A pre-baked key becomes an expired-credential
failure at a client site; an OAuth client does not expire and mints a fresh
key on every run.

Note the client ID and secret — the secret is shown once.

## 3. Store the credentials

Put them wherever that engagement's secrets live, matching the paths in
`config/management.tpl.json`:

```
overlay-network/domain          the tailnet name, or "-" for the OAuth client's own tailnet
overlay-network/client_id
overlay-network/client_secret
```

Ignition takes it from there. `management/cluster/overlay-network.tf` mints a
tagged key per run; the playbook logs the hypervisor in with it; the policy
above approves the advertised route automatically.
