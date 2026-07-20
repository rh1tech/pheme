# Pheme Mobile

Flutter application (iOS + Android) for Pheme: sign in, browse channels and live
message history, send notifications as a channel owner, manage API keys, and
register the device for push via Firebase Cloud Messaging.

It talks to the same **App API** as the web client (JWT auth). See
[../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) for the endpoint list and
[../docs/DEV.md](../docs/DEV.md) for the overall system.

## Structure

```
lib/
├── main.dart                     # bootstrap: load persisted state, init push, runApp
└── src/
    ├── app.dart                  # MaterialApp.router: theme, locale, routing
    ├── router.dart               # GoRouter with auth redirect
    ├── theme.dart                # "Iris" violet brand theme (light + dark)
    ├── core/                     # Dio client, token/settings stores, JWT, config, helpers
    ├── auth/                     # AuthController + LoginPage
    ├── channels/                 # ChannelsPage, ChannelPage + tabs (messages/send/keys/settings)
    ├── settings/                 # SettingsController + SettingsPage
    ├── data/                     # PhemeRepository + Riverpod providers (channels, live SSE)
    ├── live/                     # SSE client for /v1/stream
    ├── push/                     # FCM PushService + DeviceController
    ├── models/                   # domain models (Channel, Message, ApiKey, Device, …)
    ├── l10n/                     # English + Russian strings (key parity enforced by test)
    └── widgets/                  # shared widgets (BrandLogo, ModeBadge, ErrorView)
```

State management is **Riverpod**; routing is **go_router**; HTTP is **Dio** with
transparent access-token refresh (mirrors `web/src/lib/api.ts`).

## Configuration

The API base URL defaults to `http://10.0.2.2:8080` (the Android emulator's view
of the host) and is editable at runtime in **Settings**. On a physical device or
iOS simulator, set it to your machine's address (e.g. `http://localhost:8080`).

## Push setup (FCM)

Push is **optional** — the app degrades gracefully and still receives live
updates over SSE when Firebase isn't configured.

1. Create a Firebase project; add Android and iOS apps.
2. Place `google-services.json` (Android) and `GoogleService-Info.plist` (iOS).
   These are secrets — keep them out of version control.
3. For iOS, upload the APNs auth key to Firebase.
4. In the app, tap the bell icon on the channels screen to request permission,
   obtain the FCM token and register the device (`POST /v1/devices`).
5. Subscribe the device to a channel from the channel's **Settings** tab.

The Android build already enables core-library desugaring and declares the
`POST_NOTIFICATIONS` permission required by Android 13+.

## Encryption (MLS)

Chat and calls are end-to-end encrypted with MLS (RFC 9420). The client is **the same Rust crate the
web app uses** — `crates/pheme-mls` — reached over FFI instead of WASM, so both clients run the same
ratchet. `mobile/rust` is the flutter_rust_bridge facade over it.

Building the app builds the crate, so you need a Rust toolchain:

```bash
rustup target add aarch64-linux-android armv7-linux-androideabi x86_64-linux-android \
                  aarch64-apple-ios aarch64-apple-ios-sim aarch64-apple-darwin
cargo install cargo-ndk        # Android; the Gradle task shells out to it
```

To change the FFI surface, edit `mobile/rust/src/api/` and regenerate:

```bash
cargo install flutter_rust_bridge_codegen --version '^2'
flutter_rust_bridge_codegen generate      # from mobile/
```

The generated Dart (`lib/src/rust/`) and Rust (`mobile/rust/src/frb_generated.rs`) are committed, so a
plain `flutter build` needs no codegen step.

> The Android build does **not** use cargokit, which flutter_rust_bridge scaffolds by default. Cargokit
> calls `Project.exec()` and reads `android.applicationVariants`, both removed in Gradle 9 / AGP 9,
> which this app is on. `rust_builder/android/build.gradle` runs `cargo-ndk` instead.

## Calls

1:1 voice, peer to peer (coturn relays only when the two ends cannot reach each other). Signalling is
sealed under a key exported from the conversation's MLS group, so the server relays the SDP and cannot
read it.

**Ringing a device that is asleep needs server-side setup**, and without it calls only work while the
app is in the foreground:

- **Android** — a data-only high-priority FCM message. Works with the same `google-services.json` as
  messages.
- **iOS** — a PushKit VoIP push, which **FCM cannot send**: it is a different token, a different topic
  (`<bundle>.voip`) and a different push type. The API talks to APNs directly. Set `PHEME_APNS_KEY_FILE`,
  `PHEME_APNS_KEY_ID`, `PHEME_APNS_TEAM_ID`, `PHEME_APNS_BUNDLE_ID`, and `PHEME_APNS_PRODUCTION` to match
  how the app was signed — a token minted against the sandbox is rejected by the production gateway with
  `BadDeviceToken`, and vice versa, which is the most common reason a VoIP push silently never arrives.

Without APNs configured, an incoming call on iOS arrives as a banner rather than a ringing call screen.

## Platforms

| | Chat | Calls | Push |
|---|---|---|---|
| Android | ✅ | ✅ ringing | FCM |
| iOS | ✅ | ✅ ringing (CallKit) | FCM + APNs VoIP |
| macOS | ✅ | ✅ while the app is open | none |

macOS has no PushKit and no FCM here, so it cannot be rung while it is closed — it hears about a call
over the live stream, like the web does. It still registers a device, because the call answer-lock is
keyed on the server-issued device id: without one, the app rings and Answer does nothing.

**macOS needs signing to build.** Its entitlements include Keychain Sharing, which
`flutter_secure_storage` requires — without it, values *appear* to be written and never are, so the MLS
data key silently never persists and nothing decrypts after a restart. Open `macos/Runner.xcworkspace`
in Xcode once and set a development team.

## Develop

```bash
flutter pub get
flutter run                 # on a connected device/emulator
```

With no `PHEME_API` define the app points at `http://10.0.2.2:8080`, the Android
emulator's route to a `make dev` stack on the host (`http://localhost:8080` from
the iOS simulator).

## Building against a real server

```bash
flutter build apk --dart-define=PHEME_API=https://host.example/<path-prefix>
```

**The base URL must include the server's path prefix.** A server mounts its API
under an unlisted path with a decoy site at the document root, so the bare
hostname reaches the decoy rather than the API — a build without the prefix
fails as though the server were down.

The prefix is deliberately in no committed file; it lives in the deployment's
`stack.env` (`PHEME_PATH_PREFIX`) and nowhere else, so it has to be passed at
build time. There is no default to fall back on, which is why a release build
that forgets the define fails immediately rather than quietly reaching the wrong
host.

Users of a self-hosted instance never see any of this: they scan the QR their
operator hands them, or paste the URL, in **Settings → Server**.

## Quality gate

Before committing, all of these must pass clean:

```bash
dart format --set-exit-if-changed lib test
flutter analyze
flutter test
cargo test --manifest-path rust/Cargo.toml
```
