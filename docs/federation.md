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

**MLS credential identity becomes the qualified device URI — but not in F0.**
This is the one identity change that invalidates every existing group's ratchet
tree, and it is deliberately deferred to F5. The reasoning, recorded because it
will look like an omission otherwise:

- It buys nothing until there are cross-host groups to put remote members into,
  which is F5. Done now, it is pure risk against no benefit.
- It fights the lesson the zombie-KeyPackage incident taught. `user_of()`'s
  legacy fallback and the whole `zombieDevices` machinery exist because *mixing
  credential formats within one group* is the documented footgun — a keypackage
  under a legacy credential once burned ~500 epochs in a reconcile war.
  Introducing a second format into live single-host groups, with no federation
  to justify it, re-loads that gun.
- MIMI has not decided the credential format — the protocol draft's credential
  section is a literal `TODO`. Committing to a wire format inside the credential
  before F5 tells us what cross-host membership needs is the premature
  generality this whole document is trying to avoid.

So the credential stays `userId:deviceId` through F0–F4. F5 defines the
qualified form, migrates the trees, and handles the mixed-format transition as
one deliberate piece of work — by which time MIMI may have resolved its own
`TODO`. There is precedent for the migration itself: identities already gained a
device half once, and `user_of()` still carries that compatibility path.

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

- **F0 — Qualified identity.** `mimi://` forms (the `ident` package), `Domain`
  on users, `DirectKey` superseded by `ident.PairKey`, asymmetric JWTs with
  `iss`/`aud`/`kid`. All backwards-compatible: no data migration, and every
  change is a no-op until `PHEME_HOST_DOMAIN`/`PHEME_HOST_KEY` are set. **No
  federation yet** — local groundwork, worth doing on its own merits since the
  HS256 shared secret with no issuer claim is weak today, single-instance or
  not. **Excludes the MLS credential**, which is deferred to F5 for the reasons
  under Decision 1. *Shipped.*
- **F1 — Host identity + nodelist.** Keypair per host, signed list format,
  compiler tooling, mirroring, the application process. Self-contained.
- **F2 — S2S transport.** mTLS + signed requests, `.well-known` directory,
  starting with liveness and user-existence lookup. Proves the trust model
  without touching messaging.
- **F3 — Federated channels first.** Broadcast has no group state, no epoch, no
  ordering authority. It delivers visible cross-host value early and exercises
  F1/F2 in production before the hard part.
- **F4 — Server-inspectable handshakes.** *Shipped (epoch).* Handshake Commits
  are now `PublicMessage` (`wire_format_policy` in the pheme-mls crate, MIXED so
  the rollout needs no flag day). A ~200-line dependency-free Go decoder
  (`internal/mlswire`) reads the epoch a Commit is built on, and `postMLSCommit`
  refuses a Commit whose declared `baseEpoch` disagrees with the parsed one —
  the lie the old framing could not catch. Opportunistic: an opaque
  PrivateMessage still proceeds on its declared value, so nothing breaks during
  the rollout. NOT done: parsing the Remove proposals to enforce admin-only
  removal — that needs a server-side leaf→user map and, for real authenticity,
  the GroupContext hashes a stateless server does not have (RFC 9420 §6). Full
  commit authenticity is inherently the hub's job, which is F5. MIMI's
  SemiPrivateMessage (leaks less than PublicMessage) is the eventual target but
  depends on a younger draft OpenMLS 0.8 may not implement.
- **F5 — MLS hub model.** *Not started — this is the centerpiece, and unlike
  F0–F4 it does not decompose into a standalone increment: its parts are
  interdependent, and all of them are live cryptography. It wants a dedicated
  run with a full context budget, not the tail of another stage.* Execution
  order for that run:
  1. **Qualified credentials (F5a).** *Shipped.* `Client::new` takes the host domain; the
     MLS credential becomes `mimi://domain/d/<user>/<device>` (a form whose user
     half is itself a `mimi://domain/u/<user>`), and `user_of` parses it back to
     the qualified user. With no real users this is a CLEAN BREAK — no
     mixed-format groups, none of the zombie-KeyPackage danger that made it
     unsafe before (see Decision 1). Rebuild the WASM and the mobile FFI; the
     E2E suite is the proof that qualified credentials still encrypt/decrypt.
     Foundational: every cross-host member is named by one of these.
  2. **Signed ordering chain (F5b, Decision 2).** Each commit the hub accepts
     gets `(seq, prevHash, hash=H(prevHash‖seq‖commit), hubSig)`, stored with the
     epoch advance in the same atomic CAS (`CommitMLSGroup`). The hub key is F0's
     host key. A follower verifies the chain links and the signature. Build this
     WITH its consumer (step 4), not before — a chain nothing verifies is
     speculative.
  3. **Cross-host key-package claim (F5c).** *Shipped* — the key-package half:
     a signed S2S endpoint lets a hub claim a remote user's key packages. Remote
     membership resolution (a conversation holding a remote member) is part of
     F5d below. S2S endpoints so
     a hub can fetch a remote user's key packages (extending F2's signed
     transport) and resolve a remote member — the `chat.go` `UserByID` gate that
     rejects remote users today becomes a nodelist-aware lookup.
  4. **Hub commit-proxying (F5d).** *Plumbing built.* A follower's member builds
     a commit locally and posts it to its own host, which forwards it to the
     conversation's hub over S2S; the hub runs the F4 epoch check and the CAS,
     then fans the accepted commit back to every participant host. The relay
     layer is `chat.ConvFederation` (both the inbound `federation.Conversation-
     Service` and the outbound helper the chat handler calls) with S2S endpoints
     in `federation/conversations.go`:
       - hub → follower: `conversation-provision` (stand up a mirror),
         `conversation-relay` (deliver accepted messages/commits);
       - follower → hub: `conversation-submit-message`,
         `conversation-submit-commit` (forward a local device's post).
     A qualified `mimi://host/u/id` in the add-member endpoint records a remote
     member and provisions its host's mirror. On a mirror, `postMessage` and
     `postMLSCommit` forward to the hub instead of appending locally; on the hub
     they append and relay. The message and commit round-trips (append→relay,
     forward→order→apply-both-sides, and the epoch-conflict path) are covered by
     `chat/convfed_e2e_test.go`, wired over real HTTP through two signed
     federation handlers. Remaining: F5b's signed ordering chain (below) and the
     live two-server encrypted decrypt test.

  The hub-migration ADR this called for is written: `docs/adr-federation-hub-migration.md`
  (creator's host is the hub, immutably; hub-down pauses new events but never
  splits; permanent loss freezes the conversation read-only, with a manual
  re-home escape hatch and no automatic migration in v1).

  5. **Signed ordering chain (F5b).** *Built.* `internal/mlschain` is the
     primitive: `hash = H(prevHash ‖ seq ‖ groupID ‖ commit)` (seq = the epoch the
     commit produces, so nothing new is counted) plus a hub Ed25519 signature over
     it. The store extends the chain inside the same atomic CAS as the epoch
     advance (`CommitMLSGroup`, both backends), so `prevHash` is read and the new
     head written under one lock — the chain cannot race the order it certifies.
     The hub signs each link with its host key; a follower's `DeliverRelayed` and
     `ForwardCommit` recompute the hash from their own head, compare it to the
     hub's, and verify the signature before advancing — a mismatch (reorder, drop,
     fork) or a non-hub signature is refused and the mirror does not move. Covered
     by `mlschain/*_test.go` and `chat/convfed_e2e_test.go`
     (`TestSignedOrderingChainConvergesAcrossHosts`,
     `TestMirrorRefusesTamperedOrderingLink`).

  **On F5d's verification blocker:** the delivery service is a Go server with no
  MLS, so the *plaintext-decrypt* half of a cross-host round-trip can only be
  proven by two real clients on two servers. The message-ordering half — that the
  hub relays and orders opaque ciphertext correctly, and a mirror forwards and
  applies it — is proven in-process by `convfed_e2e_test.go` over two real signed
  handlers. The remaining two-server, two-browser E2E harness proves only that the
  bytes those tests move decrypt on the far client.

  **Verified live (2026-07-21).** F5d was proven over the wire on two separately
  deployed hosts (`test-api.example.com` as hub, `follower.example.com` as a
  follower), each a full stack with its own Mongo, sharing a signed nodelist and
  authenticating S2S with Ed25519 over TLS. `alice@hub` adding
  `mimi://follower.example.com/u/<bob>` triggered an S2S mirror provision on the
  follower (`hubDomain` correctly set); alice's hub post relayed to the mirror;
  bob's mirror post forwarded to and ordered by the hub; both hosts converged on
  one ordered log. The payloads were opaque bytes — the ordering/relay half is now
  proven both in-process and over the network; only the client-side MLS decrypt of
  those bytes remains for the two-browser harness.
- **F6 — Ordering rework.** *Foundation built; receipts + clients staged.*
    - **Done:** every message now carries `ChatMessage.Seq`, a per-conversation
      sequence the hub assigns atomically on append (`$inc` on the conversation
      doc in Mongo; a per-conversation counter in the memory store). The hub is
      the single sequencer via one rule — assign only when `Seq == 0`, so a
      message authored on a host gets the next value and one relayed from the hub
      keeps the hub's (carried on `RelayedMessage.Seq`, stored verbatim on the
      mirror). The transcript now sorts `createdAt` then `seq`, so messages
      sharing a millisecond come back in a stable order instead of a random,
      page-dependent one. Legacy messages have `seq 0` and tie among themselves as
      before. Additive and clock-agnostic; no client change needed for this part.
    - **Remaining:** turn receipts from timestamp watermarks into **sequence**
      watermarks — `domain.go`'s `DeliveredAt`/`ReadAt` are the deepest
      single-clock assumption in the codebase, and two hosts' clocks skewing would
      mark people as having read messages they never received. That change is
      client-visible (both web and mobile report and interpret receipts), so it
      needs coordinated client work and a migration, and is the one genuinely
      cross-surface piece left. Pagination cursors moving from `createdAt` to
      `(createdAt, seq)` rides along with it.

## What federation does not fix

- **Push.** FCM/APNs deliver to the OS. A federated network does not change
  which of them is reachable.
- **Metadata.** The hub sees who talks to whom and when, as the local server
  does today. Cross-host, that is now visible to *someone else's* operator too.
  That is a genuine reduction in privacy versus single-instance, and users
  should be told plainly.
- **Availability.** A room is only as available as its hub.
