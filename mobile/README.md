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

## Develop

```bash
flutter pub get
flutter run                 # on a connected device/emulator
```

## Quality gate

Before committing, all of these must pass clean:

```bash
dart format --set-exit-if-changed lib test
flutter analyze
flutter test
```
