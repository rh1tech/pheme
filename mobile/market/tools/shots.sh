#!/usr/bin/env bash
# Drive the Flutter app on a device and photograph it from the host.
#
# The Dart side prints SHOT:<name> then holds the screen still for six seconds.
# integration_test's own takeScreenshot returns the launch image on iOS, so the
# capture has to come from the platform tool, which photographs the device.
set -uo pipefail

PLATFORM="$1"; DEVICE="$2"
REPO="/Volumes/1TB/Repositories/pheme"
SCR="${SCR:-$(mktemp -d)}"
# SHOT_OUT lets a caller send the frames straight into the store package
# instead of the generic screenshots/ folder.
OUT="${SHOT_OUT:-$REPO/screenshots/$PLATFORM}"
LOG="$SCR/$PLATFORM-drive.log"

mkdir -p "$OUT"
# Deliberately NOT wiping $OUT. A run that dies half way used to take the
# previous run's good screenshots with it; each capture overwrites its own file
# and nothing else.
: > "$LOG"

case "$PLATFORM" in
  ios)     API="http://localhost:8099" ;;
  android) API="http://10.0.2.2:8099" ;;
esac

# Drop the account's server-side key backup before the run.
#
# RecoveryGate prompts to restore only when mls.session() raises
# NeedsRestoreException, and that needs a backup on the server to restore from.
# With no backup the device just starts fresh and the dialog never appears —
# which beats trying to dismiss a modal that, on a Russian device, would not
# take the tap.
SHOT_USER="${SHOT_USER:-priya}"
if command -v docker >/dev/null 2>&1; then
  docker exec pheme-shots-mongo-1 mongosh -u pheme -p pheme \
    --authenticationDatabase admin pheme --quiet --eval \
    "const u = db.users.findOne({username: '$SHOT_USER'});
     if (u) { print('cleared backups: ' + db.mlsKeyBackups.deleteMany({userId: u._id}).deletedCount); }" \
    2>/dev/null || echo "note: could not clear key backups (is the shots stack up?)"
fi

# simctl refuses to write onto the external volume the repo lives on ("Operation
# not permitted"), so capture into the scratchpad and move the file afterwards.
# Android asks for POST_NOTIFICATIONS at runtime on 13+, and the dialog lands in
# the frame. Granting it outright removes it — but the grant only works once the
# package exists, and flutter drive installs it well after this script starts. So
# keep trying in the background until it takes.
if [ "$PLATFORM" = android ]; then
  (
    for _ in $(seq 1 120); do
      if adb -s "$DEVICE" shell pm grant tech.rh1.pheme.pheme_mobile \
           android.permission.POST_NOTIFICATIONS >/dev/null 2>&1; then
        echo "granted POST_NOTIFICATIONS"
        break
      fi
      sleep 2
    done
  ) &
fi

grab() {
  # SEEDNOW is not a screenshot: it is the app telling us it is signed in and has
  # published its key packages, which is the one moment conversations can be
  # created with this device already in them.
  if [ "$1" = SEEDNOW ]; then
    if [ "${SKIP_SEED:-0}" = 1 ]; then echo "seed skipped (SKIP_SEED=1)"; return; fi
    echo "seeding conversations while the device is signed in..."
    docker exec pheme-shots-mongo-1 mongosh -u pheme -p pheme \
      --authenticationDatabase admin pheme --quiet --eval \
      '["conversations","conversationMembers","chatMessages"].forEach(c=>db[c].deleteMany({}))' >/dev/null 2>&1
    (cd "$REPO/web" && npx playwright test --config=playwright.shots.config.ts \
       e2e-shots/seed-for-phone.spec.ts --reporter=line >/dev/null 2>&1)
    echo "seeding done"
    return
  fi
  sleep 2
  local tmp="$SCR/shot-$PLATFORM-$1.png"
  if [ "$PLATFORM" = ios ]; then
    xcrun simctl io "$DEVICE" screenshot --type=png "$tmp" >/dev/null 2>&1
  else
    adb -s "$DEVICE" exec-out screencap -p > "$tmp"
  fi
  if [ -s "$tmp" ]; then
    cp "$tmp" "$OUT/$1.png" && echo "captured $1"
  else
    echo "FAILED to capture $1"
  fi
}

# Watcher first, so no marker is missed while the app boots.
(
  seen=""
  for _ in $(seq 1 900); do
    for n in $(grep -o 'SHOT:[A-Za-z0-9_-]*' "$LOG" 2>/dev/null | cut -d: -f2 | sort -u); do
      case " $seen " in *" $n "*) continue ;; esac
      seen="$seen $n"
      grab "$n"
    done
    if grep -qE "All tests passed|Some tests failed|DriverError" "$LOG" 2>/dev/null; then
      # One last sweep: the final markers are printed moments before the run
      # ends, and breaking straight away loses them.
      sleep 8
      for n in $(grep -o 'SHOT:[A-Za-z0-9_-]*' "$LOG" 2>/dev/null | cut -d: -f2 | sort -u); do
        case " $seen " in *" $n "*) continue ;; esac
        seen="$seen $n"
        grab "$n"
      done
      break
    fi
    sleep 1
  done
) &
WATCHER=$!

cd "$REPO/mobile"

# flutter drive will happily reuse a stale build.
#
# It does not always notice that the integration_test target changed, and when it
# does not it installs an old binary and runs it — the driver connects, the test
# passes, and every change you just made is absent. That failure is completely
# silent and cost hours once. If the built Dart framework is older than the
# target, throw the build away.
BUILT="build/ios/iphonesimulator/Runner.app/Frameworks/App.framework/App"
TARGET="integration_test/screenshots_test.dart"
if [ "$PLATFORM" = ios ] && [ -f "$BUILT" ] && [ "$TARGET" -nt "$BUILT" ]; then
  echo "build is older than the test target — clearing it so the change is actually compiled"
  rm -rf build/ios/iphonesimulator
fi
# Under a pty, via script(1). Redirected straight to a file, flutter's stdout is
# block-buffered, so every SHOT marker lands in the log at once when the run
# ends — by which point flutter drive has already uninstalled the app and the
# watcher photographs the launcher. A pty makes it line-buffered and the markers
# arrive while the screen still shows what they name.
python3 -c 'import pty,sys; sys.exit(pty.spawn(sys.argv[1:]))' \
  flutter drive \
  --driver=test_driver/screenshot_driver.dart \
  --target=integration_test/screenshots_test.dart \
  -d "$DEVICE" --dart-define=PHEME_API="$API" \
  --dart-define=SHOT_LOCALE="${SHOT_LOCALE:-}" >> "$LOG" 2>&1

wait $WATCHER 2>/dev/null
echo "--- markers seen ---"
grep -o 'SHOT:[A-Za-z0-9_-]*' "$LOG" | sort -u
echo "--- captured ---"
ls "$OUT"
