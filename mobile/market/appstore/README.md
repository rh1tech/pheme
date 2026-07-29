# App Store submission package — Pheme

Everything to paste into App Store Connect. Files map 1:1 to fields, so nothing
here needs rewriting on the way in.

App: **Pheme** · Bundle ID `tech.rh1.pheme.phemeMobile` · Version **1.0.0** (build 1)

## Before the first submission

Three things are not decided in this repo and have to be settled before you can
submit. They are listed here rather than guessed at, because getting them wrong
is worse than leaving them blank.

1. **Export compliance.** `ITSAppUsesNonExemptEncryption` is deliberately absent
   from `ios/Runner/Info.plist`, so App Store Connect will ask on every upload.
   Pheme is an end-to-end encrypted messenger: it uses MLS (RFC 9420) with
   AES-GCM, HPKE and Ed25519, which is more than the "authentication only"
   exemption covers. Most messengers of this shape rely on the mass-market
   exemption and file an annual self-classification report. That is a call for
   whoever is publishing, not something to hard-code on their behalf. Once
   decided, set the key so the question stops appearing.
2. **The URLs below.** All three are placeholders.
3. **App Review contact details.** See `review_notes.md` — deliberately blank,
   because this file is in a public repository.

## App Information (once per app)

| Field | Value |
|---|---|
| Primary language | English (U.S.) |
| Category | Social Networking |
| Secondary category | Productivity (optional) |
| Content rights | Does not contain third-party content |
| Age rating | See `age_rating_and_privacy.md` |

Add a **Russian** localization on the same page (Localizable Information → add
language) and fill it from `metadata/ru/`.

## Version page — per localization

| ASC field | en-US | ru | Limit | Used |
|---|---|---|---|---|
| Name | `metadata/en-US/name.txt` | `metadata/ru/name.txt` | 30 | 27 / 29 |
| Subtitle | `metadata/en-US/subtitle.txt` | `metadata/ru/subtitle.txt` | 30 | 26 / 26 |
| Promotional Text | `metadata/en-US/promotional_text.txt` | `metadata/ru/promotional_text.txt` | 170 | 155 / 148 |
| Description | `metadata/en-US/description.txt` | `metadata/ru/description.txt` | 4000 | 1922 / 1838 |
| Keywords | `metadata/en-US/keywords.txt` | `metadata/ru/keywords.txt` | 100 | 97 / 87 |
| What's New | `metadata/en-US/release_notes.txt` | `metadata/ru/release_notes.txt` | 4000 | 235 / 230 |

## URLs (both localizations)

Placeholders. Fill before submitting — Apple rejects a listing whose support URL
does not resolve.

| Field | Value |
|---|---|
| Support URL | `https://<your-domain>/support` |
| Marketing URL | `https://<your-domain>` |
| Privacy Policy URL | `https://<your-domain>/privacy` |
| Copyright | `© 2026 <copyright holder>` |

## Screenshots

Upload per device size, per localization. Order: chats → a conversation →
channels → a channel → settings.

- iPhone 6.9" (1290×2796 or 1320×2868) — **required**: `screenshots/iphone-6.9/{en,ru}/`
- iPhone 6.5" (1242×2688 or 1284×2778): `screenshots/iphone-6.5/{en,ru}/`
- iPad 13" (2064×2752) — **required, because the app ships as universal**
  (`TARGETED_DEVICE_FAMILY = "1,2"`): `screenshots/ipad-13/{en,ru}/`

If you would rather not ship an iPad build, set the device family to iPhone only
and the iPad slots stop being required. That is a product decision; the app does
currently run on iPad.

Regenerate with the capture harness in `mobile/integration_test/screenshots_test.dart`
against a seeded instance. See `../README.md`.

## Submission questions

- **Export compliance**: see the note at the top. Expect to be asked.
- **Advertising Identifier (IDFA)**: No. The app has no ads and no tracking SDKs.
- **Content rights**: No third-party content.

`age_rating_and_privacy.md` has the age-rating questionnaire and the App Privacy
answers. `review_notes.md` has the notes for App Review Information.
