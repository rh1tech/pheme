# Pheme Mobile

Flutter application (iOS + Android) that registers the device for push via
Firebase Cloud Messaging, subscribes to channels, and browses message history.

> **Not yet scaffolded.** Flutter is not installed in the current development
> environment. Once Flutter is available, generate the project in place:
>
> ```bash
> cd mobile
> flutter create --org tech.rh1.pheme --project-name pheme_mobile .
> flutter pub add firebase_core firebase_messaging http
> ```

## Planned structure
```
mobile/
├── lib/
│   ├── main.dart                # app entry + MaterialApp
│   ├── api/pheme_client.dart    # App API client (Dio/http)
│   ├── push/fcm.dart            # FCM token registration + handlers
│   ├── features/
│   │   ├── channels/            # subscribe, list
│   │   └── history/             # message history browsing
│   └── models/                  # Channel, Message, Device
├── android/  ios/               # platform projects (generated)
└── pubspec.yaml
```

## Push setup (FCM)
1. Create a Firebase project; add Android and iOS apps.
2. Place `google-services.json` (Android) and `GoogleService-Info.plist` (iOS).
   These are secrets — keep them out of version control.
3. For iOS, upload the APNs auth key to Firebase.
4. On launch, request notification permission, obtain the FCM token, and call
   `POST /v1/devices` on the App API with `{ "platform": "ios|android",
   "fcmToken": "<token>" }`.
5. Subscribe a device to a channel via `POST /v1/channels/{id}/subscribe`.

## API
The mobile app talks to the same App API as the web client. See
[../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) for the endpoint list.
