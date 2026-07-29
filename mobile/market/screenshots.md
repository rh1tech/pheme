# Regenerating the store screenshots

## What is here, and what is not

| Slot | State |
|---|---|
| App Store iPhone 6.9" (1320×2868) EN | 3 frames — chats, channels, a channel |
| App Store iPhone 6.5" (1284×2778) EN | the same three, resampled |
| App Store iPhone 6.9"/6.5" RU | **missing** — see below |
| App Store iPad 13" (2064×2752) | **missing** — no capture run yet |
| Play phone EN | 3 frames — chats, a channel, settings |
| Play phone RU | **missing** |
| Play tablet | **missing** (optional unless you declare tablet support) |

Three frames per slot is above the App Store's minimum of one and below the
maximum of ten. More would sell better; these are honest.

## Why the Russian set is missing

The capture drives the real app, and on a Russian device the app signs in, then
shows the "Восстановить чаты" dialog — a fresh install has no key store, so it
offers to restore from a backup. The driver taps "Начать заново на этом
устройстве" to dismiss it. In English that works. In Russian the tap does not
land, and every frame in the run photographed the dialog.

It is not a translation problem: the driver looks for both languages and finds
the button. Something about the tap misses it on that build. Worth ten minutes
with a debugger rather than another blind re-run.

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

## Switching a simulator to Russian

```bash
xcrun simctl shutdown <udid>
PREFS=~/Library/Developer/CoreSimulator/Devices/<udid>/data/Library/Preferences/.GlobalPreferences.plist
plutil -replace AppleLanguages -json '["ru-RU","en-US"]' "$PREFS"
plutil -replace AppleLocale -string "ru_RU" "$PREFS"
xcrun simctl boot <udid>
```
