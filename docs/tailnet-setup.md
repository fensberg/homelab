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
  // The range must cover every site you will deploy into. A single /24 is
  // fine for one site; widen it deliberately rather than by accident.
  "autoApprovers": {
    "routes": {
      "10.10.10.0/24": ["tag:homelab-router"],
    },
  },
}
```

> **Subnet collisions.** Two sites on the same tailnet advertising the same
> CIDR will collide, and traffic goes to whichever route the tailnet resolves
> first. Give every site on a shared tailnet its own subnet, and widen the
> auto-approver range to match.

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
