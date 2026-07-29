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

# simctl refuses to write onto the external volume the repo lives on ("Operation
# not permitted"), so capture into the scratchpad and move the file afterwards.
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
# Under a pty, via script(1). Redirected straight to a file, flutter's stdout is
# block-buffered, so every SHOT marker lands in the log at once when the run
# ends — by which point flutter drive has already uninstalled the app and the
# watcher photographs the launcher. A pty makes it line-buffered and the markers
# arrive while the screen still shows what they name.
python3 -c 'import pty,sys; sys.exit(pty.spawn(sys.argv[1:]))' \
  flutter drive \
  --driver=test_driver/screenshot_driver.dart \
  --target=integration_test/screenshots_test.dart \
  -d "$DEVICE" --dart-define=PHEME_API="$API" >> "$LOG" 2>&1

wait $WATCHER 2>/dev/null
echo "--- markers seen ---"
grep -o 'SHOT:[A-Za-z0-9_-]*' "$LOG" | sort -u
echo "--- captured ---"
ls "$OUT"
