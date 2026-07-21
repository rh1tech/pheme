# ADR: Federation hub selection and migration

**Status:** Accepted (v1 scope). Prerequisite for F5b/F5d in `federation.md`.
**Date:** 2026-07-21.
**Context doc:** `docs/federation.md` (Decision 5 states the summary; this is the
argument the staging plan asks for before F5b is built).

## Context

A federated conversation has one **hub**: the single host that runs the
`CommitMLSGroup` compare-and-set and so is the total order for the group. Every
other participant host is a **follower** holding a mirror and proxying its
members' posts to the hub (`chat.ConvFederation`, `federation/conversations.go`).
This is the IETF MIMI hub-provider model.

Two questions the MLS and MIMI drafts leave open, and which the code now forces
us to answer because F5d makes the hub a real, load-bearing role:

1. **Selection** — which host becomes the hub for a given conversation?
2. **Migration** — what happens when that host goes away, temporarily or
   permanently?

MIMI specifies neither: the draft carries no hub-selection algorithm, and the
string "migrat" does not appear in it. So this is ours to decide and to bound.

## Decision 1 — Selection: the creator's host is the hub, immutably

The hub of a conversation is the host of the member who created it. It is
recorded once, at creation, as the absence of `Conversation.HubDomain` on that
host (empty = "I am the hub") and its presence on every mirror (the value points
at the hub). It never changes for the life of the conversation.

Why the creator's host:

- **Simplest correct rule.** No election, no negotiation, no tie-break. The
  first `CommitMLSGroup` that establishes the group is the hub's, and every
  subsequent commit is ordered against it.
- **It matches the identifier.** A conversation's home is already encoded the way
  MIMI encodes a room's — the hub domain is a property of the conversation, not a
  separate registry to keep consistent.
- **It matches what the single-host server already did.** The hub keeps doing
  exactly what today's lone server does; followers are the only new thing. That
  keeps the highest-risk code (the CAS, the epoch guard, the F5b chain) on the
  path that is already exercised in production.

Rejected alternatives:

- **Lowest-domain / hashed selection.** Deterministic without a creator, but buys
  nothing here — there is always a creator — and it decouples the hub from the
  identifier, creating a second source of truth that can disagree.
- **Consensus / rotating hub.** Solves migration for free but replaces a CAS with
  a distributed agreement protocol. That is a different, much larger system, and
  it is precisely what the hub model exists to avoid. Not for v1.

## Decision 2 — Hub temporarily down: the conversation pauses, history stays readable

When the hub is unreachable:

- **Reads never stop.** Every participant host holds the full ordered log in its
  mirror. Members read their entire history from their own host with the hub
  offline. This falls out of the design at no cost — a mirror is a complete
  replica, not a cache.
- **New events pause.** A follower's `SubmitMessageToHub` / `SubmitCommitToHub`
  fails; the client surfaces a transient "couldn't reach the conversation" and
  retries. Nothing is lost and nothing is misordered, because unordered events
  are never optimistically applied on a follower — the follower stores only the
  hub's authoritative echo (see `ForwardMessage`/`ForwardCommit`).
- **No split brain.** Because ordering authority is singular and fixed, two
  followers cannot both accept a commit at the same epoch while the hub is down.
  The worst case is unavailability, never divergence.

This is the right failure mode for a messaging system: a conversation that is
briefly frozen and fully readable beats one that keeps accepting writes it may
later have to reconcile or roll back.

## Decision 3 — Hub permanently gone: the conversation is frozen; we say so

If the hub host is decommissioned, its key removed from the nodelist, or simply
never returns, the conversation stops accepting new events **permanently**. Its
history remains readable by every participant forever, because they all hold it.

We do **not** ship automatic hub migration in v1, and the reason is structural,
not schedule:

- The hub domain is part of the conversation's identity. Moving the hub changes
  that identity, which every mirror and every client has recorded. A migration is
  therefore a *re-creation with history import*, not a field update — the same
  reason MIMI leaves it open rather than an oversight.
- Migration also needs a trigger nobody can forge: "the hub is gone" must be a
  fact the network agrees on, not a claim any follower can make (or a partition
  can fake), or a follower could steal a live conversation by declaring the hub
  dead. That agreement is a consensus problem, back to the thing the hub model
  avoids.

So v1's answer is explicit and honest: **a hub is a single point of failure for
*new events* in the conversations it hosts.** This belongs in the user-facing
docs, not buried here. The mitigation an operator has today is operational, not
protocol: keep the hub host alive, and back it up (`docs/DEV.md` /
`deploy/self-host`).

### The escape hatch, if we ever need one

A manual, client-driven re-home is possible without new protocol, and is the path
we would take before building automatic migration:

1. A member creates a **new** conversation on a live host (a new hub, new
   identity).
2. The client imports the old conversation's readable history into it (the MLS
   history-offer machinery, `mls/history`, already moves sealed history between
   devices).
3. Members are re-added via the ordinary cross-host add flow (F5d).

This is a new group with new key material — forward secrecy is preserved, and the
old ciphertext stays decryptable only by those who already held its keys. It is a
deliberate user action, not an automatic failover, which sidesteps the forge-able
-trigger problem entirely. Whether to smooth this into a one-tap "move
conversation" flow is a **future** decision; v1 only needs to not preclude it, and
it does not.

## Consequences

- **F5b builds on a fixed hub.** The signed ordering chain (`seq`, `prevHash`,
  `hash`, `hubSig`) is signed by the hub's host key, which is stable for the life
  of the conversation — no key-rotation-mid-chain case to handle in v1. A verifier
  checks the chain against the hub domain recorded on the mirror.
- **`HubDomain` is immutable once set.** Nothing in the codebase should offer to
  change it; a "move" is a new conversation (above). Worth a comment on the field.
- **User-facing docs must state the limitation.** "If the host that started a
  group goes away for good, the group becomes read-only; its history stays." Track
  this as a docs task for the self-host README, not just this ADR.
