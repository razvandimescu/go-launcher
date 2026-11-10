#!/usr/bin/env bash
# Captures the splash demo as a GIF by taking periodic window screenshots.
# Requires: ffmpeg, swift (macOS)
#
# Usage: ./_example/splash-demo/capture.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

OUT="docs/splash-demo.gif"
FRAMES_DIR="/tmp/splash-frames"
rm -rf "$FRAMES_DIR"
mkdir -p "$FRAMES_DIR"

echo "==> Building splash demo..."
go build -o /tmp/splash-demo ./_example/splash-demo/

echo "==> Launching splash demo in background..."
/tmp/splash-demo &
DEMO_PID=$!
sleep 0.8

# Find the splash window ID by looking for the demo process's windows
echo "==> Finding splash window..."
WIN_ID=$(swift -e '
import Cocoa
let options = CGWindowListOption(arrayLiteral: .optionOnScreenOnly, .excludeDesktopElements)
guard let list = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else { exit(1) }
for w in list {
    let pid = w[kCGWindowOwnerPID as String] as? Int ?? 0
    if pid == '"$DEMO_PID"' {
        if let num = w[kCGWindowNumber as String] as? Int {
            print(num)
            break
        }
    }
}
')

if [ -z "$WIN_ID" ]; then
  echo "error: could not find splash window"
  kill "$DEMO_PID" 2>/dev/null || true
  exit 1
fi
echo "==> Found window ID: $WIN_ID"

# Capture frames at ~15 fps for the duration of the demo
echo "==> Capturing frames..."
FRAME=0
while kill -0 "$DEMO_PID" 2>/dev/null; do
  FNAME=$(printf "%04d" "$FRAME")
  screencapture -x -l "$WIN_ID" -o "$FRAMES_DIR/${FNAME}.png" 2>/dev/null || true
  FRAME=$((FRAME + 1))
  sleep 0.067  # ~15 fps
done

wait "$DEMO_PID" 2>/dev/null || true
echo "==> Captured $FRAME frames"

if [ "$FRAME" -lt 5 ]; then
  echo "error: too few frames captured"
  exit 1
fi

PALETTE="/tmp/splash-palette.png"
echo "==> Generating GIF..."
ffmpeg -y -loglevel error -framerate 15 -i "$FRAMES_DIR/%04d.png" \
  -vf "scale=iw:ih:flags=lanczos,palettegen=stats_mode=diff" "$PALETTE"

ffmpeg -y -loglevel error -framerate 15 -i "$FRAMES_DIR/%04d.png" -i "$PALETTE" \
  -lavfi "scale=iw:ih:flags=lanczos[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle" \
  "$OUT"

rm -rf "$FRAMES_DIR" "$PALETTE" /tmp/splash-demo
SIZE=$(du -h "$OUT" | awk '{print $1}')
echo "==> Done: $OUT ($SIZE)"
