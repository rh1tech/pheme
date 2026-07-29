# Data safety form — Pheme

Play Console → App content → Data safety.

Worth reading rather than clicking through: Play compares this form against what
the app actually does on the network, and a mismatch is a policy strike rather
than a warning. "End-to-end encrypted" does not let you answer "no data
collected" — encryption decides who can read the content, not what the server
records.

## What the server actually holds

- An **email address** per account, for registration, sign-in and password reset
- A **display name** and **username** the person chooses
- An optional **avatar image**
- **Message ciphertext**, which the server cannot read, and the routing metadata
  it can: conversation membership, timestamps, sizes
- **Push tokens** for FCM
- **Channel subscriptions** and channel post history

## Answers

**Does your app collect or share any of the required user data types?** → Yes

### Personal info

| Data type | Collected | Shared | Processed ephemerally | Required | Purpose |
|---|---|---|---|---|---|
| Name | Yes | No | No | Required | App functionality |
| Email address | Yes | No | No | Required | App functionality, Account management |
| User IDs | Yes | No | No | Required | App functionality |

### Photos and videos

| Data type | Collected | Shared | Processed ephemerally | Required | Purpose |
|---|---|---|---|---|---|
| Photos | Yes | No | No | Optional | App functionality |

Photos are only collected in the sense that they are uploaded, encrypted, to be
delivered to the other people in a conversation. The server cannot open them.

### Messages

| Data type | Collected | Shared | Processed ephemerally | Required | Purpose |
|---|---|---|---|---|---|
| Other in-app messages | Yes | No | No | Required | App functionality |

### App activity

Not collected. There is no analytics SDK, no event logging to a third party, and
no crash reporter in the build.

### App info and performance

Not collected.

### Device or other IDs

| Data type | Collected | Shared | Processed ephemerally | Required | Purpose |
|---|---|---|---|---|---|
| Device or other IDs | Yes | No | No | Required | App functionality |

The FCM registration token, so push notifications can be delivered.

## Security practices

- **Is all user data encrypted in transit?** → Yes. HTTPS between clients and
  server, and between federated servers.
- **Do you provide a way for users to request data deletion?** → Yes, if your
  deployment does. The app can delete an account; make sure the privacy policy
  says how, and give a deletion URL if Play asks for one.
- **Has your app been independently validated against a global security
  standard?** → No, unless you have actually commissioned one. Do not tick this
  because the protocol is a standard; the question is about an audit of this
  app.

## Data shared with third parties

**None.** Firebase Cloud Messaging is a processor for push delivery, not a
sharing relationship — Play's definition of "shared" is transfer to a third
party for their own purposes, which does not apply. Declare FCM in the privacy
policy anyway; people reasonably want to know Google is in the delivery path.

## A note for whoever fills this in

These answers describe the software as it stands in this repository. If your
deployment adds anything — server-side analytics, log retention that keeps IP
addresses, a crash reporter, an abuse-reporting pipeline that stores content —
the form has to change with it. The form describes your instance, not the
project.
