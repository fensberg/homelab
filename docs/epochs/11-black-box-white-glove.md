# Epoch 11 — Black Box, White Glove

- **Tier / path:** an estate for each client; `modules/applications/`
- **Branch:** `epoch/11-black-box-white-glove`
- **PR:** #
- **Status:** Not started
- **Closed:** `<date>`

## Goal

Sell what this repository builds to people who want it and have neither the
know-how nor the time to set it up. A client buys a machine that arrives as
their own Kubernetes cluster with none of the work, chooses what runs on it
from a menu, and changes that choice whenever they like. The operator's
business is the menu and the hardware.

The operator, on 2026-10-10: "I want there to be a core infra part that runs
on the machine and then I want clients to choose from a menu of apps that
they want installed on their hardware. Their selection pulls in that module
and it runs on their hardware and if they ever need more hardware they come
and order it from me and I can set it up for them. Their selection can
change whenever they want (i.e. today they have app A, B, and C - tomorrow
they chose A, C, and D and 'it just works'). So my business is to create
the app menu and provision the hardware."

And what kind of service it is: "Black Box, White Glove service. You buy
from me and you get into my walled garden. This is selling to folks that
want custom software without the know-how or time to set it up themselves.
I'm not saying I'm doing anything novel - just convenient."

At the end of this epoch a second party runs on hardware the operator
provisioned, on a selection they made themselves, and can do nothing to it
but use it.

## What it stands on

Most of the shape is already here, built for other reasons:

- **A core every site runs, and applications a site opts into.** An
  application is one directory and one block for each site that runs it;
  removing it touches nothing else ([03](03-workload.md)).
- **An estate is a blast radius.** Everything a client's machine holds opens
  nothing outside its own estate ([02](02-abstraction.md)).
- **A site's capacity is read and a change is held to it**
  ([04](04-observability.md), criterion 6). That is what turns "this
  selection does not fit" into "order more hardware" before anything is
  deployed.
- **A watchman outside every site** says when one goes quiet
  ([04](04-observability.md)), which is also how a machine taken off its
  management is noticed.
- **The safety officer** knows what must outlive what
  (`scripts/safety-officer`), which is where an application's data on being
  deselected starts.

## Decisions

All agreed on 2026-10-10 unless it says otherwise.

### Each client is an estate of their own

Not a site in the operator's estate. Total separation, at the price of a
whole second copy of the apparatus for each: its own organisation, vault,
break-glass key and object storage. A client's machine then holds only its
own secrets, and the rule this repository already keeps - prefer worthless
over unreachable - is the answer to a client who gets everything off their
own box: what they hold is their own data and keys that open nothing else.

### A client chooses through a web page

"Clients select via a fancy, interactive web UI (sort of like the talos
factory)." The page proposes a selection; whether it fits is the capacity
gate's to say.

### Hardware is shipped, hosted or rented, all by the operator

"Each of those 3 makes perfect sense." They differ in who holds the box and
who owns it, which is what decides how much rests on engineering and how
much on a contract:

| Model   | Who holds it | Whose it is  | What protects it                        |
| ------- | ------------ | ------------ | --------------------------------------- |
| Hosted  | the operator | the operator | the client is never near it             |
| Rented  | the client   | the operator | the seal, and the right to take it back |
| Shipped | the client   | the client   | the seal, and that the service stops    |

### A blank machine becomes a client's site at the operator's bench

Not at the client's, and not by the client. The machine leaves built and
tested.

### The hypervisor stays

Offered: the cluster's operating system straight onto the machine, with no
general-purpose system underneath to log in to. Refused, for what is being
sold: "we 100% need hypervisor on the blackbox. Selling 'your own
kubernetes cluster with none of the work' would be the tag line. We can
just lock it down so they can't get in." Several machines on one box is
what makes it a cluster: updates with nothing down, and one application's
failure not being another's. **It is not protection against the box itself
failing,** and is never to be sold as that; a second box is.

### The worry is a client taking the setup and running it without the operator

More than a client tampering with a box. Asked which, the operator: "taking
the setup and running it without me." So what is protected is the garden
and not the recipe:

- **On a client's box:** software that is running, their data, and enough to
  keep running. No credential to anything underneath, so it can be used and
  not read, changed or copied.
- **Never off the operator's side:** the menu, how each application is put
  together, what builds and updates them, the watching, the copies kept
  offsite, and the keys.

A client who leaves keeps a machine that works today and goes stale. An
honest limit, said at the time: most of what a menu would hold is open
source, and somebody capable could always install it themselves. The
protection is that leaving costs more than staying.

### The best of both, as far as it goes

The operator: "Can't we get the best of both worlds? Keep a public repo -
make tampering a black box impossible - make installing on a new, unknown
box impossible?" Mostly, with one word changed. **Impossible holds against
the people this is sold to, and not against a laboratory with the box on a
bench.** What was proposed and not refused:

- **The code may stay public.** Nothing here ever depended on it being
  secret, and a client being able to read what is in a sealed box without
  being able to change it is worth something.
- **An unknown box gets nothing.** Each machine makes an identity inside its
  own security chip at the bench, which cannot be copied off it. What serves
  applications, configuration and secrets answers only to machines that were
  enrolled. The code can be run elsewhere; the garden cannot be entered.
- **A copied disk opens nowhere else,** being sealed to the chip it was made
  beside.
- **The hypervisor is sealed:** no account that can log in at the machine,
  no recovery or single-user start, its console and remote access only on
  the operator's private network, disks that unlock only for a start nobody
  tampered with, and firmware that starts nothing unsigned and nothing from
  a stick.
- **Each estate pulls its applications with a credential of its own,** which
  can be taken away. A client who stops paying keeps what runs and is sent
  nothing new. Software that turns itself off was offered as a thing not to
  build: it turns off for clients who pay whenever the operator's own side
  is down.

**Its costs, said with it.** A disk sealed to an untampered start has to be
sealed again at every kernel and firmware update, and needs a recovery key
in the operator's vault, or one bad update strands a box nobody can reach.
And a hypervisor is more to keep patched than no hypervisor.

### A subscription is a certificate only the operator can renew (2026-10-11)

Thought through and not built. It began as a smaller question - why the
dashboards were about to be reached over plain HTTP - and became what a
subscription is made of.

**Nothing serves plain HTTP.** Offered a guard that the dashboards never
would, the operator: "The guard shouldn't be the dashboard never serves
plain HTTP - it should be that NOTHING serves plain HTTP." Two things
answer it, since two kinds of thing are served. Whatever a person or a
device reaches is served with a certificate every browser already trusts,
for a name under the operator's domain that resolves to a private address.
Whatever only the cluster's own parts say to each other is encrypted by
the network between machines, which is one setting of the network and not
a certificate for each of them; it touches every machine and the packet
size that was pinned for the tunnel, so it is a step by itself. A game
server that speaks its own protocol over UDP is neither, and the guard
says so and does not pass it in silence.

**The renewal is the subscription.** Told that such a certificate is
renewed every ninety days: "The 90 day renewal could be one of the core
parts of the business model. A subscription runs out and your cert
expires. I think we should build it the correct way now vs the half way
now." And of how far it reaches: "If a client doesn't renew then they
aren't just 'not getting dashboards' their entire estate is bricked."

**How long one lasts is the issuer's to offer.** Let's Encrypt, approved by
the operator, issues for ninety days, forty-five, and about six. **Chose**
forty-five, renewed monthly, so a client who stops is stopped between a
fortnight and six weeks later and a renewal that fails has a fortnight of
slack; and six days for a trial on a server the operator rents and
provisions. Exactly thirty would want another issuer and was not pursued.

**Only the operator can renew, and that part can be guaranteed.** "Me,
[the operator], is the only one that holds the power to renew." A
certificate for a name under a domain is issued to whoever can change that
domain's DNS, so:

- the credential that can change it is never in any estate, any site or
  anything shipped, and a guard refuses whatever would hand it to one, as
  one already does for the key that opens the backups;
- the domain itself names the one account that may be issued to, in a
  record the issuer honours, so that proving control some other way gets
  nobody a certificate;
- and the operator's own things have a place no estate's token can read,
  which does not exist yet. The domain held for the proof is in a vault
  every site reads, as a proof's is allowed to be and the real one's is
  not.

**What cannot be had, said to the operator and accepted.** A domain that
serves pages is not a secret: every certificate is published with its
names, and a browser has to be told the name to use it. "Fine - it can be
read publicly (to those who actually know how to read it)." So what is
kept is the credential and not the name, and a name never carries what a
site is called.

**And a certificate is the licence, not the lock.** "This is all a stupid
gimmick unless we can LOCK DOWN and GUARANTEE the 'only [the operator] can
renew' ... no client can ever inject their own cert." A client who holds the machine
can stand something of their own in front of it, under a certificate of
their own, and have it overlook that the operator's has run out. Nothing
in a certificate prevents that. What prevents it is the box: it reads the
certificate as proof that the operator renewed, stops itself when there is
none, and cannot be changed by whoever has it on their desk. That is the
sealed box decided above - a start nothing unsigned takes part in, disks
that open only for it, no account at any layer - and it is where the
guarantee lives. Without it this is the gimmick the operator called it.

**This turns over something this record proposed the day before**, and
does so knowingly. "Software that turns itself off" was offered as a thing
not to build, since it turns off for clients who pay whenever the
operator's own side is down. The operator has now chosen it. So its cost
is carried and not avoided: a renewal that does not run stops every
estate at once, the operator's among them. It is watched and alerted on
long before a certificate runs out ([04](04-observability.md), criteria
12 and 22), and the operator alone can extend one by hand. Of running it:
"All of those logistical challenges seem easy to operationalize and
automate. The actual 'work' will be negligible when it's up and running."

**A lapse stops an estate and hands back what is the client's.** The
operator first: "Maybe we should just rig the entire estate to 'explode'
and then plop out the database sort of like what demolish does now." And
what it is for: "at the end of the 45 days the client is left with a piece
of hardware they paid for and their data, unencrypted, sitting and waiting
for them on the hard drive ... If they choose to renew then it's super
easy just to re-assume them and if they don't then it's all accessible
for them to do whatever they want with." **Chose** stop and export, with
the operator's agreement, **over** a teardown: what runs is halted, the
client's data is written out in the clear where they can take it, and
nothing else is destroyed. A client who renews is taken up again with
nothing to rebuild. And a teardown that fires on a missed renewal fires
one day because the operator's renewal failed, with nobody watching, which
is the teardown this estate refuses everywhere else. This answers what was
open below: a client leaves with their data.

**What a proof would show**, on the second domain, when it is built: the
operator's account is issued a forty-five day certificate for a site's
dashboards; the domain refuses every other account; a guard shows no
estate holds the means; the site serves it; and an attempt from inside the
site to be issued one fails.

**Where it stood when this was written.** The dashboards had an address
of their own and the estate could route an enrolled device to it. The
Service that would have answered there over plain HTTP was written and
held back, and was never merged.

### The licence is the operator's own, and a client's domain is the client's (2026-10-12)

A day of building the section above, and then of taking it apart. What
follows turns over parts of it, each named where it does. None of it is
built, and by the operator's decision none of it is built in epoch 04.

**What was built, and set aside.** A program that asked Let's Encrypt for a
forty-five day certificate for a site's names under the operator's domain,
proved each by a record there, bound the domain to one account at the
issuer, and wrote the certificate into the site's vault (closed and not
merged, as pull request #703). It worked against stand-ins for everything
it reached. What it showed was how much had to be held to make it work.

**The operator holds nothing of a client's.** Handing a certificate over
through a client's vault means reading and writing that vault, and naming
what a client serves means a list of it. The operator: "Me as a business
has no reason to ever read a client's vault or see client's data. I set up
the structure and then hand it off - if they ever need tech support they
let me know and can one-off share." And of the list: "Ideally I don't have
one giant JSON of every client and all of their devices sitting in a repo.
That sounds like a bad infosec setup." So what the operator keeps for a
subscription is the least that renews one: a public key and the day it
runs to. No routes, no addresses, no machines, no vault.

**How a certificate reaches a box.** Five ways were set out: through the
client's vault and a converge; through the vault and a controller that
copies it in; published where the box already fetches its releases;
pushed into the cluster through the tunnel; or the box asking for itself
and the operator answering for it. **Chose** the third. The box makes its
own key and the key never leaves it; the box is enrolled once, by its
request for a certificate, which is not a secret; what the operator
publishes after that is public too, and arrives the way every other
update does. The operator: "It makes it so that the client can't bring in
their own hardware and must go through me as well." **Over** the vault,
which is the reading this decision refuses; **over** a push, which is a
credential into every client's cluster; and **over** the box asking for
itself, which needs a service of the operator's that is always up and
stops every client's renewal when it is not.

**Where the operator's side runs.** A private repository of the
operator's: the signing key as a secret there, a step only the operator
approves that enrols a box, and a schedule that renews. It is the place
above every estate that was open below. A client needs no repository:
"I'm happy to have a private repo but I don't necessarily want to force a
client to have a repo themselves."

**A client's domain is the client's, and everything they see is under
it.** The names were first to be under the operator's domain, since
whoever controls a domain can be issued a certificate for it. That made
the client's own domain useless to them, and the operator turned it over:
"A client's personal domain is important because that gives them a
significant number of options to add into their black box. They can get
their own mail server or website or whatever. Domain is important and
it's the client's identity - we should brick the box on the business side
but support everything openly on the client side." So there are two
things, and they answer to two parties. What a client's box serves is
under the client's domain, with certificates the box gets for itself,
with the client's own credential, and the operator has no part in it.
What the operator issues is the licence and nothing else.

**So the licence is not a certificate anybody's browser reads.** Once
nothing is served under the operator's domain, a publicly trusted
certificate has nothing to do. **Chose** a certificate signed by a key of
the operator's own: the box is built knowing the operator's public key,
checks that it holds a certificate from it that has not run out, and
stops itself when it does not. **Over** Let's Encrypt, because of what
that needed and this does not: a credential for a domain's records, a
record binding the domain to an account, an issuer's choice of how long a
certificate lasts, and two vendors between the operator and every
renewal. And because "only the operator can renew" is then one fact about
one key. Its costs, said with it: the key is the most valuable thing the
operator has, since losing it renews nobody and leaking it renews
anybody; and the operator's domain, which would have been in every
client's names, is in none, so changing it costs nothing.

**What this turns over in the section above.** That every site's names
are under the operator's domain; that the certificate a site serves is
the licence; that the issuer is Let's Encrypt and the length is the
issuer's to offer; and the proof described on a second domain. What
stands: the renewal is the subscription, forty-five days and a shorter
one for a trial, only the operator renews, a lapse stops an estate and
hands back what is the client's, and the sealed box is the lock - without
it a client who holds the machine takes the check out.

**Looked at and not taken:** the vendor holding every certificate at its
own edge, with each thing a client reaches given a name on a tunnel in
the operator's account. It is the least to build and has no hand-off at
all. It also lets the vendor read every client's traffic, puts a public
name on what has no login of its own, and rests the business on one
vendor's free plan.

**And none of it belongs to the epoch it was being built in.** The
question it began as was why the dashboards were about to be served in
the clear. The operator, ending it: "We were trying to fit a square peg
into a round hole when we should have just used a simple dedicated tool."
Epoch 04 serves what a device reaches over HTTPS under the estate's own
domain, with the tool made for that, and holds no licence and nothing of
an operator's. This estate is then the first client in the one way that
matters here: its names are under its own domain.

## Open

Left open on purpose by the operator, 2026-10-10: "Leave those two
decisions open."

- **Whether the menu stays in the public repository** or moves to a private
  one, with the platform under it staying open either way.
- **Which licence the public part carries.** Public need not mean free to
  resell; a licence that forbids offering it commercially is recourse
  against a competitor and nothing against a hobbyist, who was never a
  customer.

Raised and not yet answered:

- **What happens to an application's data when it is deselected.** Dropping
  one for another is easy. Wanting it back next month with what it held is a
  promise about keeping.
- **What "fits" means on the hardware a client has.** Epoch 04's rule is
  that work which cannot wait fits with one worker gone.
- **A client leaving with their data.** Proposed as a promise made outright:
  their data is theirs, and the setup is not. It is what makes a sealed box
  fair. _Answered 2026-10-11:_ a lapse stops the estate and writes their
  data out in the clear for them. How, and where on the machine, is not
  designed.
- **What a client is told** when their own site is unwell, and what only the
  operator is.
- **Where the operator's own things are kept.** _Answered 2026-10-12:_ a
  private repository of the operator's, which holds the signing key and
  runs the renewals. How the key is kept there, and whether a copy is kept
  off it, is not designed.
- **How a box is enrolled:** how its request reaches the operator at the
  bench, and what a rebuilt box does about its key.
- **What checks the licence,** and what it stops. It binds nobody until
  the box is sealed.
- **What a box needs of a client's domain** to serve under it: which
  credential, kept where, and what a client with no domain is given.
- **What a stopped estate still does.** Whether it can be reached at all,
  what it says to whoever opens it, and how the operator takes it up again.
- **What happens if the operator is gone.** A sealed box nobody else can
  service is a risk to the client too; some keeping of their own estate's
  keys by a third party.
- **Proof of what a machine started,** given by its security chip and
  checked from outside. A later tier.

## What being a black box asks of the operator

Everything. A client cannot check anything for themselves, so:

- **a restore that has been proved** is no longer a nicety
  ([04](04-observability.md), criterion 21);
- **every client's alerts reach the operator**, since they reach nobody
  else;
- **an estate has to be cheap to stand up**, because each client is one.

## Deferred

- **A cost for each workload and for a change before it merges** ("this
  will cost $5 over the next month to implement"). An idea from
  [04](04-observability.md)'s criterion 28, which collects what the site
  draws; pricing it is this epoch's or a later one's.
