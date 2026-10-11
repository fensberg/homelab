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
Jamie, is the only one that holds the power to renew." A certificate for a
name under a domain is issued to whoever can change that domain's DNS, so:

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
gimmick unless we can LOCK DOWN and GUARANTEE the 'only jamie can renew' ...
no client can ever inject their own cert." A client who holds the machine
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

**Where it stood when this was written.** The dashboards have an address
of their own and the estate can route an enrolled device to it. The
Service that would answer there over plain HTTP was written and is held
back for this.

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
- **Where the operator's own things are kept:** the domain's credential and
  the account certificates are issued to. A place above every estate, which
  no estate's or site's token reads. Needed before the first real
  certificate, and not for the proof.
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
