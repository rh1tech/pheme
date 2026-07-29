# The network nodelist

The federated network is a set of instances that trust each other's word. That
trust is anchored in one signed document — the **nodelist** — which maps each
member's domain to its public key. Every instance mirrors the same document.

It is faithful to FidoNet's nodelist: compiled and signed centrally, mirrored
everywhere. Admission is a reviewed change to a file. See
`docs/federation.md` for the current trust and operating model.

## The coordinator

One party runs the coordinator: they hold the signing key and decide who is
admitted. Every instance is configured with the coordinator's **public** key, so
a list is trusted because the coordinator signed it — not because of where it was
downloaded from.

```sh
pheme-nodelist init                       # once — creates coordinator.key
pheme-nodelist pubkey                      # the value every host is given
```

`coordinator.key` is the one secret. Its public half is meant to be published.
Losing the private half means re-keying the whole network, so back it up.

## Admitting, rotating, removing

A joining operator sends their host's **public** key — the "nodelist entry" line
from `pheme-hostkey`. The coordinator adds it:

```sh
pheme-nodelist add    talk.example.com  <their-host-pubkey>
pheme-nodelist add    talk.example.com  <new-pubkey>   # re-add = rotate in place
pheme-nodelist remove talk.example.com                 # this is how revocation works
```

`roster.json` is plain, diffable JSON — admission and removal are reviewable
changes to it. Then sign and publish:

```sh
pheme-nodelist sign --days 30 > nodelist.json
```

Every signing bumps a monotonic **serial**, so a host refuses to be fed an older
list — a rollback that would re-admit a removed host. The list also carries an
**expiry**: an instance that falls out of contact with the coordinator stops
vouching for anyone once its list ages out, so removal always takes effect. Sign
on a cadence shorter than the expiry (e.g. weekly, 30-day expiry).

## Distributing it

Publish `nodelist.json` anywhere every host can fetch it — it is signed, so the
channel does not have to be trusted. Each host points `PHEME_NODELIST_PATH` at a
local copy and sets `PHEME_NODELIST_COORD_KEY` to the coordinator public key:

```sh
PHEME_NODELIST_COORD_KEY=<coordinator pubkey>
PHEME_NODELIST_PATH=/opt/pheme/nodelist.json
```

Both must be set to join the network. Setting one without the other is treated as
a misconfiguration and the host stays standalone — with a warning, not a silent
guess. A host that means to stand alone sets neither.

A host that is federated but whose nodelist is missing or malformed **refuses to
start**, rather than coming up silently trusting no peers: an operator who
configured federation deserves an error, not a mystery.

## Exposing the S2S endpoints

Peers reach a federating host at unprefixed paths — `/.well-known/pheme-federation`
and `/federation/v1/*` — because a peer discovering this host cannot know its
secret path prefix. Re-render the nginx vhost with `PHEME_FEDERATION=1` so those
locations are proxied to the app:

```sh
PHEME_FEDERATION=1 PHEME_API_HOST=... ./deploy/nginx/render.sh > ...
```

Without that flag the block is omitted entirely, and for a good reason: those
paths would otherwise proxy to an app with no federation configured, which
answers Go's `404 page not found` — visibly different from the decoy's nginx 404,
and so a fingerprint. A standalone host has no federation block and every such
path falls through to the decoy like anything else. Exposing the endpoints is
only appropriate once a host has joined a network, at which point it is already
publicly listed by domain in the nodelist.

## What this is and is not

It is the trust and revocation layer: who is a peer, and which key speaks for
them. It is **not** the transport (that is F2) and it says nothing about message
ordering (that is F5). A host with a loaded nodelist knows who its peers are and
can verify their signatures; it does not yet talk to them.
