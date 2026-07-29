# External join: a new device joins an existing group without a reset

## The problem

An MLS group is made of leaves, one per device. A brand-new device (a freshly installed phone) has
no leaf in a conversation's existing group, and a device **cannot add its own leaf with an ordinary
commit** — only an existing member can. So today a new device has two ways in:

1. **Wait to be admitted** — announce itself and hope a member device is online to admit it. Smooth
   when someone is online; a dead wait when nobody is.
2. **Rebuild the group** — retire it and build a new one. This is destructive: it works only if every
   other party promptly migrates to the new group, and when they do not, the two sides end up on
   different groups and cannot read each other. (We hit exactly this.)

Neither is good. The right mechanism is the one MLS defines for precisely this case.

## The mechanism: external commit (RFC 9420 §11.2.1)

OpenMLS 0.8.1 supports it directly:

- `MlsGroup::export_group_info(crypto, signer, with_ratchet_tree=true)` — a **member** produces a
  signed `GroupInfo` describing the group's current epoch, ratchet tree, and an `external_pub` key.
- `MlsGroup::join_by_external_commit(provider, signer, ratchet_tree, group_info, …)` — a **non-member**
  consumes that `GroupInfo` and produces an **external commit** that adds its own leaf. It requires no
  Welcome and no help from any member: the joiner adds itself.

Two properties make this safe and correct:

- **It is non-destructive.** The group is not rebuilt; one leaf is added. Every existing member keeps
  every key. If the joiner's old leaf is still present (a ghost), the external commit removes it in the
  same step.
- **It goes through the same compare-and-set as any commit.** The external commit carries the epoch it
  was built against. If another commit landed first, the server refuses it and the joiner refetches the
  `GroupInfo` and retries. No divergence is possible.

## Why the authorization already holds

An external commit lets *anyone holding the GroupInfo* join. That is only safe if the GroupInfo is
served exclusively to people entitled to be in the group. It is: **the joiner is already a member of
the conversation roster** — that is why they can open the chat at all — they simply have no MLS leaf
yet. The server already gates `GET/POST /mls/*` on conversation membership, so serving GroupInfo to a
roster member, and accepting an external commit from one, needs no new trust — it is the same gate the
ordinary commit path already uses.

A non-member of the conversation can neither fetch the GroupInfo nor post the commit. A member's leaf
still carries its signed `userId:deviceId` credential, verified by every other member on processing —
identical to the key-package path.

## The three layers

### Rust (`crates/pheme-mls` + `mobile/rust`)

Two new methods on the shared `Client`, both additive (existing exports unchanged):

- `export_group_info(group_id) -> bytes` — for a group this client holds, return the serialized
  `GroupInfo` (with ratchet tree). Persist nothing; it is derived state.
- `join_by_external_commit(group_info) -> Staged{ commit, state }` — build the external commit and the
  local group. The returned group has a pending commit that MUST be merged once the server accepts it,
  and MUST be discarded whole on refusal (external commits cannot be selectively rolled back). So the
  façade models it as: `join_by_external_commit` stages, `commit_accepted` merges, `commit_rejected`
  drops the whole group.

A Rust unit test proves the loop: client A creates a group; client B, a non-member, external-joins from
A's exported GroupInfo; A processes B's commit; both encrypt to and decrypt from the shared group.

### Server (`api/internal/chat`)

- Store the latest `GroupInfo` per group. A member uploads fresh GroupInfo whenever it commits
  (establish, add, remove) — the material a future joiner needs is then always one epoch behind at
  worst, and a stale GroupInfo simply loses the compare-and-set and triggers a refetch.
  - `POST /v1/conversations/{id}/mls/group-info` — body: `{ groupId, epoch, groupInfo }`. Members only.
  - `GET  /v1/conversations/{id}/mls/group-info` — returns the latest. Members only.
- The external commit itself needs **no new endpoint**: it is a commit, and rides the existing
  `POST /mls/commit` compare-and-set. The only change is that the sender need not already hold an MLS
  leaf — which the roster gate already permits.

### Client (`mobile/lib/src/crypto/mls_service.dart`)

`_settleGroup`, on finding `established && !hasGroup(current)`:

1. `GET` the latest GroupInfo.
2. `join_by_external_commit(groupInfo)` → external commit.
3. Post it via the existing commit compare-and-set.
   - Accepted → merge, we are in the group. One round trip. No wait, no reset, nothing destroyed.
   - Refused (epoch moved) → refetch GroupInfo, retry a few times, then fall back to the current
     announce-and-wait as a last resort.

The destructive reset stays in the tree only as the true last resort for the pathological case where no
GroupInfo is available at all (e.g. every member's device lost its keys) — it stops being the answer to
the ordinary "new phone" case, which is the whole point.

## Verification (all against the local server, never production)

- Rust unit test: the external-join loop above.
- Integration test (`mobile/integration_test`): a second device opens an existing 1:1 chat with **no
  other device online**, external-joins, and reads messages sent **after** it joined — while the other
  party, still offline, is untouched and reads everything on return. This is the exact scenario that
  broke, asserted to now resolve with nobody online and no group reset.
- The existing suite must stay green — external join must not perturb establish, welcome, add, remove,
  or the group-reset path.

## Rollout

Ship the Rust + server + client together behind the ordinary flow; there is no user-visible switch.
Web can adopt the same two crate methods later; until it does, a phone external-joins a group a browser
established and vice-versa, because the mechanism is symmetric and the GroupInfo is standard MLS.
