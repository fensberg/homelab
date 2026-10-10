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
  fair.
- **What a client is told** when their own site is unwell, and what only the
  operator is.
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
