# Regenerating the store screenshots

## What is here, and what is not

| Slot | State |
|---|---|
| App Store iPhone 6.9" (1320×2868) EN | 3 frames — chats, channels, a channel |
| App Store iPhone 6.5" (1284×2778) EN | the same three, resampled |
| App Store iPhone RU | **missing** — one manual tap away, see below |
| App Store iPad 13" (2064×2752) | **missing** — no capture run yet |
| Play phone EN | 4 frames — chats, channels, a channel, settings |
| Play phone RU | 4 frames, the same screens |
| Play tablet | **missing** (optional unless you declare tablet support) |

Three frames per slot is above the App Store's minimum of one and below the
maximum of ten. More would sell better; these are honest.

## Forcing the language

Do not change the simulator's or emulator's language. The app keeps its own
preference in secure storage under `pheme.locale`, and that outranks the device
locale — so the capture writes it before calling `app.main()`:

```dart
await const FlutterSecureStorage().write(key: 'pheme.locale', value: 'ru');
```

Pass `SHOT_LOCALE=ru` (or `en`) to `tools/shots.sh` and it threads through as a
dart-define. One build photographs both languages, and nothing has to reboot.

## Two dialogs that get in the way

**"Restore your chats."** A fresh device with a backup waiting on the server is
offered a restore, and the dialog sits on top of every screenshot. Dismissing it
by tapping is unreliable. `tools/shots.sh` deletes the account's server-side key
backup before the run instead: `RecoveryGate` only prompts when `mls.session()`
raises `NeedsRestoreException`, which needs a backup to restore from, so with
none there is no dialog to dismiss.

**Notification permission.** On Android the script grants
`POST_NOTIFICATIONS` outright — in a retry loop, because the grant only works
once `flutter drive` has installed the package.

On iOS there is no equivalent. `simctl` has no tap command, `simctl privacy`
does not cover notifications, and AppleScript clicking needs an Accessibility
permission the runner does not have. The alert appears on every fresh install,
and `flutter drive` uninstalls the app when it finishes, so it comes back every
run. **This is why the App Store Russian set is missing.** The fix is one manual
tap: run a capture, press "Allow" on the simulator once, and every later run on
that install is clean. The English set was captured that way.

## Why there is no login screenshot

The iOS simulator draws its keyboard tutorial ("Speed up your typing by sliding
your finger…") over the sign-in form on a fresh device, and it lands in the
frame. A sign-in screen is a weak opener for a store listing anyway.

## Running a capture

The app needs a seeded instance to photograph. Bring one up:

```bash
# infrastructure
docker compose -f deploy/docker-compose.yml -p pheme-shots up -d
# API + dispatcher against it, plus the seeded accounts
cd api && PHEME_STORE_DRIVER=mongo PHEME_BROKER_DRIVER=rabbit ... go run ./cmd/app
cd api && ... go run ./cmd/dispatcher
```

Then drive a device. The Dart side prints `SHOT:<name>` and holds the screen
still; the host photographs it with `xcrun simctl io` or `adb exec-out screencap`,
because `integration_test`'s own `takeScreenshot` returns the iOS launch image
rather than the app.

```bash
SHOT_OUT=mobile/market/appstore/screenshots/iphone-6.9/en \
  bash tools/shots.sh ios <simulator-udid>
```

Conversations are seeded *during* the run, not before it. This matters and is
not an optimisation: `flutter drive` uninstalls the app when the run ends, which
destroys the MLS key store, so a device never carries keys from one run to the
next. Anything seeded beforehand is ciphertext that device has no way to read,
and every row in the chat list says "Encrypted message" — correct behaviour,
useless screenshot.

## Device sizes

Create the simulators the store sizes need:

```bash
xcrun simctl create "Pheme 6.9"     com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro-Max      <runtime>
xcrun simctl create "Pheme iPad 13" com.apple.CoreSimulator.SimDeviceType.iPad-Pro-13-inch-M5-12GB <runtime>
```

iPhone 17 Pro Max renders at 1320×2868, which is an accepted 6.9" size with no
resampling. The 6.5" frames here are resampled from it, which Apple accepts.

