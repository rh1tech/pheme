# Federation

Pheme federation connects independently operated instances while preserving
client-side MLS encryption. It is **permissioned federation**: servers join a
network by appearing in a coordinator-signed nodelist. It is not an open network
where any domain can begin sending requests.

## What is federated

- user lookup by qualified identifier or network host alias;
- subscriptions and delivery for open broadcast channels, including processed
  images;
- conversation provisioning and full ciphertext mirrors;
- messages, MLS commits, key-package claims, and sequence-based receipts;
- encrypted call signalling, ringing nudges, and short-lived TURN credentials.

Approval-mode channel membership and comments on mirrored channel posts are not
currently federated.

## Network trust

Each host has an Ed25519 keypair. A coordinator maintains a roster mapping
domains and optional short aliases to public keys, signs the complete list, and
publishes it. Every member configures the coordinator public key and a local copy
of the signed list.

The list carries an issue time, expiry, and increasing serial:

- the signature proves the coordinator approved its contents;
- expiry prevents an isolated server from trusting removed peers forever;
- the serial supports rollback prevention;
- removing a host and publishing a new list revokes that peer.

Admission is centralized even though hosting and data are distributed. This is
a deliberate spam/abuse boundary, not a claim of fully decentralized governance.

## Host-to-host discovery and authentication

A federated host exposes:

```text
GET /.well-known/pheme-federation
/federation/v1/*
```

The discovery document advertises implemented endpoint paths. Requests are sent
over HTTPS and signed with the origin host key. Signatures bind method, path,
origin, destination, key id, timestamp, nonce, and body hash. The receiving host
looks up the origin in its verified nodelist and rejects unknown, expired,
replayed, stale, misaddressed, or invalid requests.

These endpoints are deliberately unprefixed because a peer cannot discover a
private client API prefix. A standalone host should not proxy them at all; nginx
then serves the same decoy response as any unknown path.

## Conversation hub model

The host of the member who creates a conversation becomes its immutable hub.
The hub:

- owns the authoritative ordered ciphertext log;
- assigns message sequence numbers;
- performs MLS epoch compare-and-set;
- signs the MLS ordering chain;
- relays accepted events to participant hosts.

Other participant hosts are followers. A follower stores a complete mirror for
local reads, forwards local writes to the hub, verifies the returned order and
signature, and persists only the authoritative echo.

### Availability consequence

If the hub is temporarily unreachable, history remains readable from every
mirror but new events pause. Followers do not accept writes independently, so
the network favors consistency and avoids split brain.

If the hub disappears permanently, the conversation becomes read-only in v1.
There is no automatic migration. Moving a conversation would require a new
conversation identity, new MLS group, explicit history import, and member
re-addition. Operators must treat every hosted conversation as durable state and
back up MongoDB and the host identity.

## Join an existing federation

Joining requires coordination. Obtain the network's admission policy, current
nodelist URL, coordinator public key, and contact method from its coordinator.

### 1. Deploy and verify a standalone server

Complete [deployment.md](deployment.md). The API domain must have working HTTPS,
correct time synchronization, persistent MongoDB/Redis, and an off-host backup.
Do not request admission for an unverified or temporary host.

### 2. Generate the host identity

From the repository:

```bash
cd api
go run ./cmd/hostkey -env
```

Store the emitted `PHEME_HOST_KEY` seed with the deployment secrets. Derive the
public key by running the tool without `-env`; send only the public key, desired
domain, and optional alias to the coordinator.

The domain must match `PHEME_HOST_DOMAIN` and the externally reachable
federation hostname. Keep the private seed out of tickets, chat logs, nodelists,
and Git.

### 3. Obtain admission

The coordinator adds the domain and public key, signs a new nodelist with a
higher serial, and publishes it. Confirm that the issued document contains the
exact domain and public key before enabling federation.

### 4. Store and configure the nodelist

Fetch the signed document to a stable host path:

```bash
sudo install -d -m 0750 /opt/pheme
curl --fail --proto '=https' --tlsv1.2 \
  https://network.example/nodelist.json \
  | sudo tee /opt/pheme/nodelist.json >/dev/null
sudo chmod 0644 /opt/pheme/nodelist.json
```

Set:

```dotenv
PHEME_HOST_DOMAIN=chat.example.com
PHEME_HOST_KEY=<private-host-key-seed>
PHEME_NODELIST_COORD_KEY=<coordinator-public-key>
PHEME_NODELIST_PATH=/nodelist.json
PHEME_NODELIST_FILE=/opt/pheme/nodelist.json
```

The production Compose stack mounts `PHEME_NODELIST_FILE` at
`/nodelist.json`. Both nodelist settings are required. A configured federated
server refuses to start with a missing, malformed, expired, or incorrectly
signed list rather than silently becoming standalone.

`PHEME_PEER_URLS` can override resolution for private networks and tests:

```dotenv
PHEME_PEER_URLS=peer.example=https://10.0.0.12:8443
```

Production normally leaves it empty and uses `https://<peer-domain>`.

### 5. Expose federation routes

Render nginx with federation enabled:

```bash
set -a
. /opt/pheme/stack.env
set +a
PHEME_FEDERATION=1 \
PHEME_API_HOST=chat.example.com \
PHEME_DECOY_DIR=example-decoy \
PHEME_SSL_SNIPPET=/etc/nginx/snippets/pheme-ssl.conf \
  ./deploy/nginx/render.sh | sudo tee /etc/nginx/sites-available/chat.example.com.conf
sudo nginx -t
sudo systemctl reload nginx
```

Restart the App API and confirm the log reports federation enabled with the
expected origin and nodelist serial.

### 6. Verify with the coordinator

From outside the host:

```bash
curl --fail https://chat.example.com/.well-known/pheme-federation
```

The liveness endpoint itself requires a correctly signed peer request; use
another admitted instance or the federation E2E harness rather than an unsigned
`curl`. Verify remote user lookup, a cross-host open-channel subscription, and a
cross-host encrypted conversation before onboarding users.

### 7. Keep membership current

Refresh the nodelist before expiry using an atomic replacement:

```bash
curl --fail https://network.example/nodelist.json -o /opt/pheme/nodelist.json.new
mv /opt/pheme/nodelist.json.new /opt/pheme/nodelist.json
docker compose --env-file /opt/pheme/stack.env restart app
```

Validate the coordinator signature and increasing serial before replacement in
automated update tooling. Coordinate host-key rotation as a new nodelist issue;
do not replace a private key before peers trust the new public key.

## Operate a federation coordinator

The coordinator tool stores `coordinator.key` and `roster.json` in its working
directory:

```bash
mkdir pheme-network && cd pheme-network
go run /path/to/pheme/api/cmd/nodelist init
go run /path/to/pheme/api/cmd/nodelist pubkey
```

Admit, rotate, or remove a host:

```bash
go run /path/to/pheme/api/cmd/nodelist add chat.example.com <host-public-key> example
go run /path/to/pheme/api/cmd/nodelist remove chat.example.com
```

Issue the list:

```bash
go run /path/to/pheme/api/cmd/nodelist sign --days 30 > nodelist.json
```

Publish `nodelist.json` over any reliable channel; authenticity comes from the
signature, but HTTPS still protects availability and reduces tampering noise.
Back up `coordinator.key` offline. Its compromise lets an attacker admit keys;
its loss requires distributing a new trust anchor to every host.

Use an admission process that verifies domain control, operator identity,
security contacts, abuse handling, backup readiness, and the submitted host
fingerprint. Keep roster changes reviewable.

## Operational checklist

- Synchronize clocks with NTP; signatures allow only five minutes of skew.
- Alert before nodelist expiry.
- Keep Redis available to every App API replica for nonce replay protection.
- Do not rewrite `/federation/v1/*` paths between signing and application.
- Retain logs for signature and relay failures without logging message bodies or
  credentials.
- Back up MongoDB and the host key off-host.
- Test peer removal and key rotation before an incident.
- Tell users that cross-host metadata is visible to more than one operator and
  that hub loss freezes new events.

Design history and implementation staging are preserved in
[`development/federation.md`](development/federation.md), with the hub decision
in [`development/adr-federation-hub-migration.md`](development/adr-federation-hub-migration.md).
