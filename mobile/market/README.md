# Store submission packages

Everything needed to publish the Flutter app, in the form the two consoles ask
for it.

```
market/
├── appstore/          App Store Connect
│   ├── metadata/{en-US,ru}/*.txt      one file per field
│   ├── screenshots/{iphone-6.9,iphone-6.5,ipad-13}/{en,ru}/
│   ├── age_rating_and_privacy.md      questionnaire + App Privacy answers
│   └── review_notes.md                what to paste into App Review
├── googleplay/        Play Console
│   ├── metadata/{en-US,ru-RU}/*.txt   one file per field
│   ├── graphics/                      icon 512, feature graphic 1024×500
│   ├── screenshots/{phone,tablet}/{en,ru}/
│   ├── data_safety.md                 Data safety form answers
│   └── content_rating.md              IARC questionnaire
└── tools/             regenerates the Play graphics from the app icon
```

Text lives in `.txt` files rather than in a spreadsheet or a wiki so it diffs,
reviews and translates like the rest of the repository. Character counts are in
each store's README, checked against the limits.

## Readiness audit

Done before writing any of the above. What follows is the state of the app
itself, not of the listing.

### Ready

| Item | State |
|---|---|
| App name | `Pheme` on both platforms (`CFBundleDisplayName`, `android:label`) |
| Version | `1.0.0+1` in `pubspec.yaml`, flowing to `$(FLUTTER_BUILD_NAME)` / `$(FLUTTER_BUILD_NUMBER)` on iOS and `flutter.versionName` / `flutter.versionCode` on Android — one number to bump, in one place |
| iOS icons | Full `AppIcon.appiconset`, 21 sizes including the 1024×1024 marketing icon |
| Android icons | Adaptive icon (`mipmap-anydpi-v26`) plus density mipmaps |
| iOS permission strings | Camera, microphone and photo library, each saying what it is actually for |
| Android permissions | Declared with `uses-feature required="false"` for camera and microphone, so the listing is not withheld from devices lacking them |
| Android signing | `key.properties` wired in `build.gradle.kts`, falling back to debug signing so a checkout without the keystore still builds. Neither the keystore nor `key.properties` is tracked, and both are gitignored |
| Languages | English and Russian, in the app and in this package |

### Fixed here

**`ios/Runner/PrivacyInfo.xcprivacy` was missing.** Apple has required a privacy
manifest for the app target since spring 2024; without one an upload draws
ITMS-91053, which turns into a rejection. Added, and registered in the Runner
target's resources build phase — a manifest that is not in the build phase is
not in the app, and that failure is silent.

It declares no tracking, the four required-reason APIs the dependencies actually
reach for, and collected data types matching the App Privacy answers. Those two
have to agree; Apple checks.

### Needs a decision before submitting

Five things this repository cannot settle.

1. **Export compliance.** `ITSAppUsesNonExemptEncryption` is deliberately not set
   in `Info.plist`, so App Store Connect will ask on every upload rather than
   silently taking an answer nobody chose. Pheme uses MLS (RFC 9420) — AES-GCM,
   HPKE, Ed25519 — which is well past the "authentication only" exemption.
   Encrypted messengers generally rely on the mass-market exemption and file an
   annual self-classification report. That is a legal call for the publisher.

2. **Store URLs.** Support, marketing and privacy policy are placeholders in
   both READMEs. Apple rejects a listing whose support URL does not resolve.

3. **Demo account.** Both stores need one, with real content in it — a direct
   chat, a group, a channel with posts. A reviewer who opens an empty app cannot
   assess the app. Credentials go in the consoles, never in this repository.

4. **The bundle identifiers differ**, and this is the last moment to change
   either:

   | Platform | Identifier |
   |---|---|
   | iOS | `tech.rh1.pheme.phemeMobile` |
   | Android | `tech.rh1.pheme.pheme_mobile` |

   Both read like Flutter's generated defaults rather than something chosen.
   Neither store cares that they differ, and nothing in the code depends on them
   matching — but they are permanent the moment either app is published, and
   `tech.rh1.pheme` would be the obvious name for both. Changing them means
   regenerating `GoogleService-Info.plist` and `google-services.json`, since
   Firebase keys its configuration on the identifier. Worth ten minutes now;
   impossible later.

5. **iPad.** `TARGETED_DEVICE_FAMILY = "1,2"`, so the app ships as universal and
   the App Store requires iPad screenshots. If you would rather launch
   iPhone-only, change the device family and the requirement goes away. It is a
   product decision, not a packaging one — the app does run on iPad today.

## Screenshots

Captured from a real client against a seeded instance, by
`mobile/integration_test/screenshots_test.dart` driving the app while the host
photographs the device. See `mobile/market/screenshots.md` for how to run it.

Chat previews on a freshly signed-in device read "Encrypted message" until that
device has been a member of a conversation for the messages in it — which is
correct behaviour for end-to-end encryption, and the reason the capture seeds
conversations *during* the run rather than before it.
