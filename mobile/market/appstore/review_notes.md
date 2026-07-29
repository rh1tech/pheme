# App Review Information

## Contact

Deliberately blank. This file is in a public repository, and App Review contact
details are a name, a phone number and an email belonging to a real person. Fill
them in App Store Connect directly, not here.

- First name: —
- Last name: —
- Phone: —
- Email: —

## Sign-in required?

**Yes.** Pheme is a messenger; there is nothing to see signed out. Provide a
demo account in App Store Connect (Demo Account → username and password), on a
server the reviewer can reach.

The demo account needs to have something in it, or the reviewer opens an empty
app and cannot assess anything. Before submitting, sign in as the demo account
and make sure it has at least: one direct conversation with a few messages, one
group, and one channel with a handful of posts. A second account you control on
the same server is enough to produce all of that.

## Notes for the reviewer (paste into the Notes field)

Pheme is an end-to-end encrypted messenger with broadcast notification channels.

SERVER FIELD ON THE SIGN-IN SCREEN. The sign-in form has three fields: email,
password, and the server address. This is deliberate and is the same idea as an
email client asking which mail server you use — Pheme is self-hosted software,
and different groups run their own instances. The demo credentials above are for
the server pre-filled in the field; you should not need to change it.

ENCRYPTION. Conversations are encrypted end to end using MLS (RFC 9420). The
server relays ciphertext and holds no key that can read it. This is why a new
device is offered a recovery code: without one, message history cannot be moved
to it, because the server cannot supply what it cannot read.

USER-GENERATED CONTENT AND MODERATION. People send each other messages and
photos. Content is private to a conversation or to a channel's subscribers;
there is no public feed, no discovery, and no way to reach a stranger's content.
Moderation is available at every level: a person can leave any conversation, a
group admin can remove members, a channel owner can ban a subscriber and delete
a post, and the server operator can suspend or delete an account.

CHANNELS. A channel is a notification feed with an HTTP endpoint — a build
server or a website posts to it and subscribers get a push notification and a
history they can scroll. It is a one-way broadcast, not a chat.

VOICE CALLS. One-to-one calls over WebRTC. Call setup is encrypted with keys
derived from the same MLS group as the conversation. Media is peer-to-peer where
the network allows, and relayed through TURN when it does not.

NO ADS, NO TRACKING, NO PURCHASES. There is no advertising SDK, no analytics
SDK, and nothing to buy. Firebase Cloud Messaging is present, for push delivery
only.

The app is in English and Russian, following the device language.
