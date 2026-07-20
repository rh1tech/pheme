# Federation: a network of self-hosted instances

Status: **design, not built.** Nothing here is implemented. This exists to settle
the decisions that have to be made before the first line, because three of them
are baked into MLS credentials and into every stored identifier — get one wrong
and it costs a second migration through the same code.

## The goal

Users on one Pheme instance can message users on another, without giving up the
property that makes the app worth running: the server cannot read messages, and
neither can anyone else's server.

Instances join a common network. Each is operated by whoever runs it. New hosts
apply to join; the host map is a signed list every instance mirrors.

## What we are following, and how loosely

The IETF **MIMI** working group exists for exactly this problem — federated
messaging over MLS — and its shape is the right one to follow. But it is not a
spec you can conform to today, and the plan this document replaces was too
confident about that. As of July 2026:

| Document | Rev | Status |
|---|---|---|
| `draft-ietf-mimi-arch` | 03 | WG document |
| `draft-ietf-mimi-protocol` | 06 | WG document, expires 2026-10-27 |
| `draft-ietf-mimi-content` | 09 | WG document, most mature |
| `draft-ietf-mimi-room-policy` | 04 | WG document |

**Nothing is an RFC.** No document shepherd, no IESG evaluation scheduled. The
credential section of the protocol draft still contains a literal
`TODO: What types of credential are required / allowed?`.

So: follow MIMI's **shape** (hub-per-room, provider-qualified identifiers, HTTPS
between providers), keep our own choices **pluggable** where MIMI has not
decided, and do not block on it settling. Where MIMI is silent — and it is
silent about a lot — the decision is ours and is recorded below.

There is no complete open-source MIMI implementation to read. The nearest thing
is [phnx-im/air](https://github.com/phnx-im/air), by two of the draft authors,
built on OpenMLS — which we already use.

---

## Decision 1 — identifiers

**Adopt MIMI's `mimi://` URI form.**

```
user       mimi://a.example/u/alice
device     mimi://a.example/d/<deviceId>
room       mimi://a.example/r/<roomId>
MLS group  mimi://a.example/g/<groupId>
```

Note this is *not* `im:user@domain` (RFC 7565) — earlier drafts used that and
the current protocol draft replaced it with its own hierarchical scheme. The
authority component carries the provider domain, which is the whole point: an
identifier says which host is authoritative for it.

### What this breaks here

| Today | Problem |
|---|---|
| `User.ID` — bare 24-char hex (`domain.go`) | Nothing distinguishes local from remote |
| `usernamePattern` `^[a-zA-Z_][a-zA-Z0-9_.]{2,29}$` | Cannot hold `@`, `:` or `/` |
| `DirectKey(a, b) = a + ":" + b` | `:` is in the qualified form; the key becomes ambiguous |
| MLS credential `userId:deviceId` (`crates/pheme-mls/src/lib.rs`) | Unqualified, and `:` again |
| JWT: HS256 shared secret, no `iss`/`aud`/`kid` | Two hosts sharing a secret cross-authenticate by bare id |

**Storage keeps local opaque ids.** Mongo `_id`s stay as they are; a `Domain`
field is added, and the qualified `mimi://` URI is the *wire* identity, derived
not stored. Rewriting every primary key would be a far larger migration for no
benefit — the qualified form matters at boundaries, not in indexes.

**`DirectKey` stops being a string join.** It becomes a hash of the two
qualified identifiers, sorted. A separator-based key cannot survive identifiers
that contain the separator, and picking a different separator just moves the
problem to whichever character the next identifier scheme uses.

**MLS credential identity becomes the qualified device URI.** This invalidates
every existing group's ratchet tree and needs a migration. There is precedent:
identities already migrated once when they gained the device half, and
`user_of()` in `lib.rs` still carries the compatibility path from it.

---

## Decision 2 — ordering integrity: **we add what MIMI dropped**

This is the decision that matters most, and the one where we deliberately do
*not* follow the current draft.

MIMI rooms have one **hub** provider that orders events; other providers are
followers that proxy their users' requests to it. That part we adopt — it maps
exactly onto what the code already does, since
`store_mongo_keypackages.go`'s single-document compare-and-set *is* a hub, just
one that currently has no peers.

But the current protocol draft has **no ordering integrity whatsoever**. Its
`FanoutMessage` carries a wall-clock timestamp and nothing else — no sequence
number, no previous-message hash, no transcript hash. Followers take the hub's
word for ordering, backed only by MLS's own epoch progression.

This is a regression from **Linearized Matrix**, the draft MIMI descends from,
which had providers "attach verifiable hashes and signatures to each event as a
safeguard against the hub server modifying the events."

**We cannot accept that.** Pheme's entire proposition is that the server is
untrusted. Today that holds: the server relays ciphertext it cannot open. Under
MIMI-as-drafted, a *remote* hub — run by someone the user has never met — could
reorder, drop, or selectively deliver messages to some participants and not
others, with no in-protocol way to detect it. MLS gives confidentiality and
epoch agreement; it does not give delivery integrity. Adopting the hub model
without compensating would quietly downgrade the one property the product is for.

So: **every fanout message carries a sequence number and a hash of the previous
message, signed by the hub.** Each follower verifies the chain and can prove a
hub equivocated. This is what Linearized Matrix did, it is not much code, and it
keeps "your friend's server cannot lie to you about what was said" true.

We should raise the gap on `mimi@ietf.org` rather than only solving it privately
— it is likely to be addressed before RFC, and being an implementer with a
concrete design is the useful way to raise it.

---

## Decision 3 — host admission: a signed nodelist

Faithful to FidoNet, and the operator's stated preference. A coordinator
compiles and signs a `domain → public key` map; every host mirrors it; new
operators apply.

The tradeoff is real and should be stated in the project's own docs rather than
glossed: **admission is centralised**, and that is a different thing from the
hosting being decentralised. What it buys is a workable answer to spam and abuse
from day one, which open federation does not have.

Each host has an **Ed25519 keypair**. The public half is its nodelist entry. It
signs S2S requests and its own JWTs (`kid` names the key, so rotation is a new
entry rather than a flag day).

---

## Decision 4 — transport

HTTPS, following MIMI: **mutual TLS**, certificates authenticating both provider
domains, discovery via `/.well-known/mimi-protocol-directory`.

One deviation to consider: MIMI dropped Matrix's per-request signatures in
favour of mTLS alone. Since we already require a host signing key for the
nodelist and for the ordering chain, signing requests with it costs little and
does not depend on the TLS terminator being the application — which matters when
half the point of this project is that hosts sit behind CDNs and reverse
proxies. **Do both.**

---

## Decision 5 — hub selection and migration

MIMI specifies **neither**. I scanned the protocol draft: no hub selection
algorithm, and the word "migrat" does not appear.

**Selection: the creator's host.** Simplest, matches the draft's examples, and
matches the room identifier already encoding the hub domain.

**Migration: unsolved, and we should say so rather than pretend.** Because the
room ID embeds the hub domain, moving a room changes its identifier — which is
very likely why MIMI leaves it open rather than an oversight. For v1 a room
whose hub disappears is a room that stops accepting new events; its history is
still readable by every participant, because they all hold it. That is a real
limitation and belongs in the user-facing docs, not just here.

---

## Staging

Each stage ships and is useful before the next one starts.

- **F0 — Qualified identity.** `mimi://` forms, `Domain` on users, `DirectKey`
  becomes a hash, asymmetric JWTs with `iss`/`aud`/`kid`. Breaking; ship behind a
  migration that accepts both forms. **No federation yet** — this is entirely
  local groundwork, and it is worth doing on its own merits: the HS256 shared
  secret with no issuer claim is weak today, single-instance or not.
- **F1 — Host identity + nodelist.** Keypair per host, signed list format,
  compiler tooling, mirroring, the application process. Self-contained.
- **F2 — S2S transport.** mTLS + signed requests, `.well-known` directory,
  starting with liveness and user-existence lookup. Proves the trust model
  without touching messaging.
- **F3 — Federated channels first.** Broadcast has no group state, no epoch, no
  ordering authority. It delivers visible cross-host value early and exercises
  F1/F2 in production before the hard part.
- **F4 — Server-inspectable handshakes.** `mlsgroup.go:264-278` already names
  this: commits are `PrivateMessage`, so the server cannot validate a claimed
  `baseEpoch` or a claimed `Removes`. It calls PublicMessage framing "the right
  fix and it is not done here." A hub validating a *remote* host's clients needs
  it. MIMI also allows SemiPrivateMessage, which leaks less — but depends on
  another individual draft, so: PublicMessage now, revisit.
- **F5 — MLS hub model.** Hub assignment, follower proxying, the signed ordering
  chain from Decision 2, cross-host key-package claim, credential migration.
- **F6 — Ordering rework.** Hub-assigned sequence numbers replacing wall-clock
  ordering. Receipts stop being timestamp watermarks — `domain.go:506-521` is the
  deepest single-clock assumption in the codebase, and two hosts' clocks skewing
  would mark people as having read messages they never received.

## What federation does not fix

- **Push.** FCM/APNs deliver to the OS. A federated network does not change
  which of them is reachable.
- **Metadata.** The hub sees who talks to whom and when, as the local server
  does today. Cross-host, that is now visible to *someone else's* operator too.
  That is a genuine reduction in privacy versus single-instance, and users
  should be told plainly.
- **Availability.** A room is only as available as its hub.
