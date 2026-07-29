# Age rating questionnaire & App Privacy answers

## Age rating (App Information → Age Rating → Edit)

Everything is None / No except the last two, which a messenger cannot honestly
answer otherwise.

- Cartoon or Fantasy Violence: None
- Realistic Violence: None
- Prolonged Graphic or Sadistic Realistic Violence: None
- Profanity or Crude Humor: None
- Mature/Suggestive Themes: None
- Horror/Fear Themes: None
- Medical/Treatment Information: None
- Alcohol, Tobacco, or Drug Use or References: None
- Simulated Gambling: None
- Sexual Content or Nudity: None
- Graphic Sexual Content and Nudity: None
- Unrestricted Web Access: **No** — the app opens no browser and renders no
  remote web content
- **Gambling and Contests: No**

Two questions need care:

- **"Does your app contain user-generated content?"** → **Yes.** People send each
  other messages and photos. Answering No would be false, and Apple treats UGC
  claims as a review item.
- **Frequency/intensity of UGC** → the app is a private messenger, not a public
  feed: content is visible only to the people in a conversation or subscribed to
  a channel. There is no discovery, no public timeline and no way to reach a
  stranger's content.

Because there is user-generated content, Apple requires the app to have, and to
show that it has, moderation affordances. Pheme's are: a person can leave any
conversation, a group admin can remove a member, a channel owner can ban a
subscriber and delete a post, and a server operator can block or remove an
account. Say so in the review notes.

Expected resulting rating: **12+** (unrestricted UGC in a messenger usually
lands here, occasionally 17+ depending on how the reviewer reads the questions).
It will not be 4+, and a submission that claims 4+ for a messenger tends to come
back.

## App Privacy (App Privacy section → Get Started)

This one deserves precision, because "end-to-end encrypted" does not mean "no
data collected". Encryption is about who can read the content, not about what
the server records.

What a Pheme server holds:

- An **email address** per account (registration, sign-in, password reset)
- A **display name and username** the person chooses
- An optional **avatar image**
- **Message ciphertext**, which the server cannot read, plus routing metadata
  it can: who is in a conversation, when messages were sent, message sizes
- **Push tokens** for APNs/FCM
- **Channel subscriptions** and channel post history

Recommended answers:

1. "Do you or your third-party partners collect data from this app?" → **Yes**.

2. Data types:

   | Type | Linked to identity | Used for | Tracking |
   |---|---|---|---|
   | Contact Info → Email Address | Yes | App Functionality | No |
   | User Content → Photos or Videos | Yes | App Functionality | No |
   | User Content → Other User Content (messages, ciphertext) | Yes | App Functionality | No |
   | Identifiers → User ID | Yes | App Functionality | No |
   | Contact Info → Name (display name) | Yes | App Functionality | No |

3. "Do you use data for tracking?" → **No.** Nothing is shared with data brokers
   or used for cross-app advertising. There is no advertising SDK and no
   analytics SDK in the build.

The honest framing for the privacy policy: the server stores ciphertext and the
metadata needed to deliver it, and holds no key that can open the ciphertext.
Do not claim the server "stores nothing" — it stores plenty, it just cannot read
the part that matters.

If the person publishing runs the only server, that is their data-controller
relationship to describe. If the app points at instances run by other people,
the privacy policy should say that too: which server someone signs in to
determines who holds their metadata.

## Third-party SDKs

Firebase Cloud Messaging is in the build, for push on Android and for
non-VoIP notifications on iOS. It receives a device token and delivery payloads.
It is not used for analytics, and Firebase Analytics is not linked. Mention FCM
in the privacy policy; it is a processor, not a partner you share data with for
tracking.
