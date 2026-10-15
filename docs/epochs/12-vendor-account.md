# Epoch 12 — Vendor Account

- **Tier / path:** `management/estate/`
- **Branch:** none; every piece branches from `main`
- **PR:** #
- **Status:** Not started
- **Closed:**

## Goal

The account at the vendor that carries an enrolled device to a site, answers
the estate's private names and holds its domain is described by this
repository, whole, and tightened. At the end nothing in that account was set
by hand and left for nobody to read, and what the estate relies on the
vendor for is written beside what it deliberately does not.

It is its own epoch because it is its own chunk of work. The account is
older than the repository and holds two years of settings made by hand.
Epoch 04 came to rely on it for a private name and a certificate, and each
time a setting nobody had declared turned out to matter, it was found by
something breaking.

## Scope

In scope:

- Everything in the account that nothing here declares, each declared or
  removed: the DNS rules and lists made by hand, a tunnel from before the
  estate made them, the API tokens (#717).
- The account-wide settings for what the vendor does with a device's
  traffic, declared and set from git. The first of them is below.
- The domain: which issuers may issue for it, what the credential a site
  proves its names with can reach, and that no private name has a public
  record (#717).
- Who is a member of the account, and how a device comes to be enrolled
  under an address the estate's list does not hold (#717).
- Why an enrolled device is given no address for a private name by the
  vendor's own route for it (#708). The estate answers those names with a
  rule of its own in the meantime.

Explicitly out of scope (and which epoch owns it instead):

- Who may reach what, by name and by person: Epoch 09.
- Inspecting web traffic at the house's gate (#731): Epoch 09, and not the
  vendor's at all.
- The settings of the estate's source control: Epoch 09 (#729).

## Decisions

### The vendor does not open what a device sends to the estate (decided 2026-10-13)

**Chose:** the vendor's TLS decryption is off for the account, and stays
off while the only traffic the vendor carries is the estate's own.

**Because:** it was found switched on, by a browser refusing a dashboard's
certificate. The certificate offered was the vendor's, made on the spot,
and not the one the estate holds. Three things were true at once:

- An enrolled device sends the vendor only the estate's private addresses.
  Nothing from the internet passed through, so decryption protected the
  device from nothing out there. It opened one kind of traffic: the
  operator's own sessions with the estate.
- No rule used what was opened. Decryption is what lets a rule look inside
  a connection, and there was no such rule.
- It broke the rule Epoch 04 set for the layers: the vendor gets a device
  in and holds no certificate, and the estate ends HTTPS with its own. With
  decryption on, the vendor read every session in the clear and the
  estate's certificate was seen by the vendor and by nobody else. The guard
  for that rule reads what the estate declares, and this was a setting
  nobody had declared.

It was turned off by hand on 2026-10-13 and the estate's certificate
reached the browser at once.

**Still to do, and the first work of this epoch:** declare it off in the
estate, so that it is set from git and found if it is ever otherwise; stop
the vendor's own certificate being installed on enrolled devices, which
nothing needs once nothing is opened; and have the guard fail on either
being declared on. The resource that holds the setting holds several others
of the account's, so what stands is read first and the change is checked
with a plan of the estate before it is applied.

**Rejected:** leaving it on with the estate's names exempted. That is the
right shape for an account that carries a device's whole traffic. This one
carries none of it, so there would be an exemption for everything that
passes and inspection of nothing.

**If the estate later carries all of a device's traffic** through the
vendor, inspection there becomes worth having, and it is turned on with the
estate's own names left unopened. The operator's preference is to inspect
at the house's gate instead (#731).

## Outcome

## Deferred

## Gotchas

- A browser that keeps its own list of authorities refuses the vendor's
  certificate; one that uses the machine's list accepts it without a word,
  because the vendor's client had put the vendor's authority there. The
  quiet case is the worse one. To see who really issued a certificate, look
  at it.
