# Protocol and security model

Pheme uses separate protocols for client access, broadcast ingestion, MLS
delivery, and server-to-server federation. This document describes the current
contract and trust boundaries; route handlers remain the implementation source
of truth.

## Trust model

- TLS protects traffic between a client and its selected server.
- Conversation content and call signalling are encrypted on clients with keys
  derived from MLS. Servers are untrusted delivery services for those bytes.
- Broadcast channels are not E2EE. Their content is processed, stored, and
  delivered by the server.
- Servers see account, membership, device, routing, timing, IP, and delivery
  metadata. Federation exposes relevant cross-host metadata to the conversation
  hub and participant hosts.
- Push providers receive delivery metadata and notification payloads needed by
  the configured client experience.

## Identities

Local MongoDB identifiers remain opaque. Federation uses provider-qualified
identifiers whose authority names the home server:

```text
mimi://chat.example.com/u/<user-id>
mimi://chat.example.com/d/<user-id>/<device-id>
mimi://chat.example.com/r/<conversation-id>
mimi://chat.example.com/g/<group-id>
```

The form follows the MIMI architecture's provider-qualified shape, but Pheme's
federation protocol is project-specific and should not be presented as MIMI
interoperability.

## Client authentication

Registration and password reset use short-lived verification codes. Passwords
are stored with Argon2id. Authenticated APIs use short-lived bearer access tokens
and rotating refresh tokens.

Federation-ready servers sign tokens asymmetrically with their Ed25519 host key.
Tokens include issuer and audience bound to the host domain. Legacy HS256
verification can be enabled only until an explicit transition deadline.

Channel ingestion is a separate authority: `X-Api-Key` can publish to one
channel but cannot act as a user. API keys are high-entropy values stored only as
hashes and shown once when created.

## HTTP API families

All client APIs are JSON over HTTPS except multipart uploads, SSE, and opaque
binary attachment responses.

| Route family | Authentication | Purpose |
|---|---|---|
| `/v1/auth/*` | Public, rate-limited | Register, verify, login, refresh, and password reset |
| `/v1/me`, `/v1/users/*` | User JWT | Profile and user discovery |
| `/v1/channels/*` | User JWT | Channel ownership, membership, posts, comments, keys, and subscriptions |
| `/v1/ingest/{channel}/notify` | Channel API key | External broadcast trigger |
| `/v1/devices` | User JWT | Push-device registration |
| `/v1/conversations/*` | User JWT + membership | Conversation roster, ciphertext messages, attachments, receipts, MLS, and calls |
| `/v1/mls/*` | User JWT | Device key packages, device registry, and encrypted key backup |
| `/v1/stream` | Access token query parameter | SSE live event stream |
| `/v1/admin/*` | Admin JWT | Instance administration and moderation |
| `/.well-known/pheme-federation` | Public only on federated hosts | Federation endpoint discovery |
| `/federation/v1/*` | Signed peer request | Host-to-host services |

The SSE token is carried in the query because browser `EventSource` cannot set an
Authorization header. Operators should avoid logging query strings containing
credentials.

## Broadcast protocol

`POST /v1/ingest/{channelId}/notify` accepts JSON text fields or multipart data
with images. At least one content field is required. `Idempotency-Key` scopes
deduplication to the channel and allows a caller to retry a timed-out request
without intentionally creating another notification.

The success response means RabbitMQ confirmed the publication, not that every
device received push. MongoDB history is authoritative; push delivery is
best-effort and recorded separately.

Images are decoded at the API boundary, stripped of source metadata through
re-encoding, resized, and stored by opaque blob identifier. Federation carries
processed bytes to the subscriber host because the origin's client API may live
behind an unlisted path.

## MLS conversation protocol

Pheme uses MLS (RFC 9420) through OpenMLS. One MLS leaf represents one device,
not one user. Every device owns private key material and publishes public
KeyPackages for other group members to claim.

### Commit ordering

Membership changes are staged on a client:

1. Build a commit against the current epoch without merging it locally.
2. Submit the commit and base epoch to the conversation hub.
3. The hub performs a compare-and-set against the current epoch.
4. Merge only after acceptance; discard the staged state after rejection.
5. Fetch accepted commits and retry from the new epoch when another commit won.

Handshake commits use a server-inspectable MLS wire form so the hub can verify
the declared base epoch. Application messages remain opaque.

### Sender authentication

Decrypting an application message yields the plaintext **and** the credential
MLS authenticated as its signer (`mimi://<domain>/d/<user>/<device>`), plus the
epoch it was framed in. Clients attribute messages by that credential — for the
author name, own-message placement, quoted-message authors, conversation-list
previews and notification previews — never by the `senderId` on the envelope,
which the untrusted delivery service writes. Where the two disagree the client
renders an explicit unverified state rather than picking a name. Cached messages
decrypted before sender attribution existed keep an unverified compatibility
fallback to the envelope, and are never presented as verified.

### New devices and history

A roster member whose new device has no group leaf can join through an MLS
external commit using a member-published `GroupInfo`. The same compare-and-set
prevents concurrent commits from forking the group.

The server stores ciphertext history but cannot decrypt it for a new device.
Existing devices can upload an encrypted history handoff. Encrypted key backups
support device recovery without giving the server the plaintext keys.

The handoff is sealed under the group's exporter secret, which proves only that
the sender is *a* member — every member derives the same secret. So requests and
offers are additionally **signed with the member's MLS leaf key** over a
canonical, domain-separated transcript (`pheme/mls/history-{request,offer}/v2`)
and verified against the leaf key the group's ratchet tree holds for the claimed
identity. The request transcript binds the conversation, group, epoch, requester
and a fresh nonce; the offer transcript additionally binds the offerer, the
history id, the AEAD salt and nonce, the request nonce, and a SHA-256 digest of
the encrypted blob — so the server cannot swap the blob behind a valid offer.
Unsigned v1 bodies are refused with no fallback. Clients also check the claimed
identity against the poster the server authenticated on the control message.

A leaf signature proves which group member offered the bytes, but any participant
can sign fabricated history as themselves. Clients therefore provide and accept
history only between devices of the same domain-qualified account. Other group
members are never eligible history providers.

Transferred message bodies carry the MLS-authenticated sender of each original
message, and are marked as relayed on import, so a new device never adopts
attacker-chosen plaintext under someone else's server-supplied `senderId`.

### Messages, attachments, and receipts

The hub assigns a monotonic sequence to each accepted conversation message.
Mirrors store that sequence unchanged. Pagination and delivery/read receipts use
sequence watermarks rather than wall-clock order, avoiding cross-host clock skew.

Conversation attachments are encrypted by the client before upload. The server
stores opaque blob bytes and authorizes retrieval through conversation
membership. Broadcast images use a different, server-readable pipeline.

## Calls

Clients derive a signalling key from the current MLS group with the MLS exporter
and seal SDP/ICE signalling before sending it. The server relays those bytes and
cannot alter the DTLS fingerprint without detection by the receiving client.

Media travels directly over WebRTC where possible. TURN is a relay of last
resort. The API mints short-lived TURN credentials from a server-held shared
secret; the secret itself is never returned.

## Federation transport

Peers communicate over HTTPS. Each request is additionally signed with the
sending server's Ed25519 host key and verified against the signed nodelist.
Pheme does not currently depend on application-terminated mutual TLS.

The canonical signature binds:

```text
scheme
HTTP method
request path
origin domain
destination domain
key identifier
Unix timestamp
random nonce
SHA-256(body)
```

Receivers allow at most five minutes of clock skew and record a valid nonce for
twice that window. The replay store must be shared by all App API replicas. A
missing or unavailable replay store fails closed for signed peer requests.

The destination is not accepted from a caller-controlled header: the receiver
binds its own configured domain. This prevents a valid request for one peer from
being forwarded to another.

## Federation ordering

One server is the hub for each conversation. It sequences events and accepts MLS
commits. Followers forward local writes and apply only the hub's signed result.
Accepted commits carry a chain link derived from the previous hash, sequence,
group identifier, and commit bytes, signed by the hub key. A follower rejects a
gap, reorder, fork, changed body, or signature from a non-hub key.

This gives detectable ordering integrity but not hub availability. See
[federation.md](federation.md) for operational consequences.

## Protocol stability

The client and federation APIs are evolving with the project. Deploy server and
client releases that are documented as compatible, and test mixed-version
upgrades before production. The historical decisions and migration notes under
[`development/`](development/) are useful when changing MLS or federation
formats.
