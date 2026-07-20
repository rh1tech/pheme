# Running your own Pheme instance

One host, one domain, a certificate, and Docker. `setup.sh` generates everything
else.

```sh
cd deploy/self-host
./setup.sh
```

It asks for a hostname and an admin email, generates every secret, picks a decoy
site, renders the nginx vhost, and prints the URL to hand to your users. Follow
the steps it prints, then check your work:

```sh
./verify.sh https://talk.example.com <path-prefix>
```

Run `verify.sh` from somewhere that is not the server, and run it again after
any nginx change.

## Why this exists

A single deployment on a single domain is one firewall rule away from gone. Many
instances on many domains, each with its own certificate and its own decoy, share
no fingerprint and have no common point of failure. That is the whole idea: your
instance is not a mirror of somebody else's, it is its own.

To that end the instance is built not to announce itself. The API lives at an
unlisted path; everything else on the host is an ordinary static site. A scanner
sweeping address space finds a small website and moves on.

**The prefix is not a password.** Every endpoint behind it authenticates exactly
as it would otherwise. The prefix is in the URL of every request, so any CDN or
middlebox terminating TLS in front of you can see it. It is *unlisted*, which
defeats automated discovery — not secret, which would be a different claim.

## Edit the decoy

`setup.sh` picks one of the shipped decoys at random. **Change it before you go
live.** Three or four sites that ship with Pheme, served verbatim, are a
fingerprint rather than a disguise — recognising them is easier than recognising
Pheme was in the first place. `verify.sh` fails until you have.

It does not need to be elaborate. A page about a real interest of yours, a
business, a club, anything with a plausible reason to exist at that domain.

## What you have to decide

**Mail.** The default (`PHEME_MAIL_DRIVER=log`) prints verification codes to the
container log instead of emailing them:

```sh
docker compose --env-file node.env logs app | grep -i code
```

That is enough to register yourself and a few people you know. For anything
larger, point `PHEME_SMTP_*` at a relay. Mail you send from a fresh domain will
land in spam until SPF, DKIM and DMARC are in place.

**Push notifications.** Web Push works out of the box — `setup.sh` generates the
VAPID keys. **Mobile push does not, and cannot, without work only you can do:**

- Android needs your own Firebase project, its `google-services.json`, and the
  app rebuilt with it.
- iOS needs an Apple developer account, an APNs key, and your own bundle
  identifier.

Without them the mobile app still works fully while it is open, and receives
nothing while backgrounded. This is a real limitation of self-hosting, not a
configuration you have missed. Users should know it.

**Voice calls.** 1:1 calls need a TURN relay for peers behind symmetric NAT.
Leave `PHEME_TURN_URLS` empty and calling is switched off, which is better than
a feature that works for some pairs and fails for others. To enable it, run
coturn (see `../prod/turnserver.conf` for a configuration that is not an open
relay) and set the URLs and shared secret.

**The web app.** Off by default. Start it with `--profile web` and set
`PHEME_WEB_ORIGIN`. Understand the trade: a served web app is identifiable by its
content wherever it sits, so serving it gives up most of what the decoy buys you.
A mobile-only instance is the quiet configuration.

## Onboarding users

Give them the URL `setup.sh` printed — hostname plus prefix:

```
https://talk.example.com/a7f3c91e4b2d
```

In the app: **Settings → Server**, then paste it, or tap **Scan** if you have a
QR. Install `qrencode` before running `setup.sh` and it prints one to the
terminal and writes a PNG.

Hand it over out of band. Anything that publishes it — a public page, a search
index — undoes the point of it being unlisted.

## Looking after it

`node.env` holds every secret and is mode 0600. **Back it up.** Losing
`PHEME_JWT_SECRET` signs everyone out; losing `PHEME_PATH_PREFIX` makes the
instance unreachable until you re-render nginx and tell every user.

Rotating the prefix means changing `PHEME_PATH_PREFIX` and `PHEME_API_BASE`
together, re-rendering nginx, and restarting. Mobile clients keep the old URL in
secure storage until their owner changes it, so keep the old prefix mounted
alongside the new one until they have.

Upgrades are `docker compose --env-file node.env pull && ... up -d`. Read the
release notes first; this project has had migrations.

## What this does not protect against

- **Someone who already knows the prefix.** It defeats discovery, not a targeted
  adversary who has your URL.
- **IP blocking.** Nothing here helps if your host's address is nullrouted.
- **Traffic-analysis of the client.** The mobile app's TLS fingerprint is not a
  browser's, so an adversary profiling TLS handshakes can distinguish app traffic
  to your host from ordinary browsing to it. Fixing that is open work.
- **Anything about content.** Messages are end-to-end encrypted with MLS and the
  server cannot read them, but it does see who talks to whom and when. That was
  true before any of this and is unchanged.
