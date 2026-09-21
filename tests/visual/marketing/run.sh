#!/bin/bash
# Regenerate the landing page's media from a real rekam instance.
#
#   tests/visual/marketing/run.sh
#
# Builds the app, runs it against a throwaway data dir on port 3011, seeds the
# demo corpus, drives the UI with Playwright (in the same Docker image the
# visual suite uses, so the browser build matches), then compresses everything
# with ffmpeg and installs it into frontend/public/media/.
#
# Re-run it whenever the UI changes enough that the shots stop being true. The
# output is committed, because a landing page cannot depend on a laptop having
# Docker to render.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VISUAL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PROJECT_DIR="$(cd "$VISUAL_DIR/../.." && pwd)"
DATA_DIR="$SCRIPT_DIR/.capturedata"
RAW_DIR="$SCRIPT_DIR/.raw"
MEDIA_DIR="$PROJECT_DIR/frontend/public/media"
PORT=3011
DOCKER_IMAGE="mcr.microsoft.com/playwright:v1.53.0-noble"

export REKAM_ADMIN_EMAIL="admin@rekam.local"
export REKAM_ADMIN_PASSWORD="rekam-capture-admin"
export REKAM_REGISTRY="$DATA_DIR/registry.sqlite"
export REKAM_DEMO_EMAIL="${REKAM_DEMO_EMAIL:-ada@rekam.local}"
export REKAM_DEMO_PASSWORD="${REKAM_DEMO_PASSWORD:-catalog-of-records}"

for tool in ffmpeg jq docker; do
  command -v "$tool" >/dev/null || { echo "ERROR: $tool is required"; exit 1; }
done

echo "━━━ rekam marketing capture ━━━"
rm -rf "$DATA_DIR" "$RAW_DIR"; mkdir -p "$DATA_DIR" "$RAW_DIR"
mkdir -p "$MEDIA_DIR"

echo "→ Building frontend..."
( cd "$PROJECT_DIR/frontend" && npm run build --silent )
echo "→ Compiling rekam binary..."
( cd "$PROJECT_DIR" && go build -o "$DATA_DIR/rekam" ./cmd/rekam )

echo "→ Starting rekam on port $PORT..."
"$DATA_DIR/rekam" serve --registry "$REKAM_REGISTRY" --addr ":$PORT" >"$DATA_DIR/server.log" 2>&1 &
SERVER_PID=$!
cleanup() {
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR"
}
trap cleanup EXIT

for _ in $(seq 1 40); do curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null && break; sleep 0.25; done
curl -s -o /dev/null "http://localhost:$PORT/" || { echo "ERROR: server did not start"; cat "$DATA_DIR/server.log"; exit 1; }

echo "→ Seeding demo corpus..."
bash "$SCRIPT_DIR/seed.sh" "http://localhost:$PORT"

if [ ! -x "$VISUAL_DIR/node_modules/.bin/playwright" ]; then
  echo "→ Installing Playwright runner..."
  ( cd "$VISUAL_DIR" && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install --silent --no-save "@playwright/test@1.53.0" )
fi

# Every theme rekam.THEMES (frontend/src/lib/store.js) offers, shot as its
# own full pass. 'light' has no suffix — it's the bare default everywhere
# else in the app too — everything else is its own name.
THEMES=(light dark malleable)
suffix_for() { [ "$1" = light ] && echo "" || echo "-$1"; }

echo "→ Capturing..."
# There is no theme-agnostic recording of an app that has a theme, so each
# one gets its own full pass: light and dark drive Playwright's own
# colorScheme, and any explicit-only theme (malleable) is set the same way a
# real visitor's saved choice would be (see capture.js). All land under
# distinct names and the landing page picks between them at runtime.
for THEME in "${THEMES[@]}"; do
  echo "  · $THEME"
  docker run --rm --network host --user "$(id -u):$(id -g)" -e HOME=/tmp \
    -e REKAM_DEMO_URL="http://localhost:$PORT" \
    -e REKAM_DEMO_EMAIL -e REKAM_DEMO_PASSWORD \
    -e REKAM_CAPTURE_THEME="$THEME" \
    -e OUT_DIR=/output \
    -v "$VISUAL_DIR:/tests" -v "$RAW_DIR:/output" -v "$SCRIPT_DIR:/marketing" \
    -w /tests "$DOCKER_IMAGE" node /marketing/capture.js
done

echo "→ Encoding stills (webp)..."
# Wide shots carry the whole app, so they get room; the feature shots sit in a
# narrower column and would only be paying for pixels nobody sees. hero-poster
# is sized to match the hero clip's own frame width (CLIP_VIEW × CLIP_SCALE in
# capture.js) since it stands in for one of that clip's frames, not a still.
still() { # still <name> <target-width>
  ffmpeg -y -loglevel error -i "$RAW_DIR/$1.png" \
    -vf "scale=$2:-2:flags=lanczos" -c:v libwebp -quality 80 -compression_level 6 \
    "$MEDIA_DIR/$1.webp"
  printf '  %-20s %s\n' "$1.webp" "$(du -h "$MEDIA_DIR/$1.webp" | cut -f1)"
}
for THEME in "${THEMES[@]}"; do
  s="$(suffix_for "$THEME")"
  still "record$s"      2400
  still "search$s"      2400
  still "mindmap$s"     2400
  still "hero-poster$s" 2560
done

echo "→ Assembling video from frames..."
# capture.js streams the clips through Chrome's screencast at 2x rather than
# letting Playwright record: Playwright rasterises video at CSS-pixel size and
# ignores deviceScaleFactor, so its recordings are half the resolution the page
# needs. The screencast is damage-based, so it writes a concat list carrying
# each frame's real duration and that is what gets assembled here.
#
# CRF is low for video on purpose. This is screen content: the detail that
# matters is text and one-pixel rules, exactly the high-frequency detail a high
# CRF discards first.
OUT_FPS=24
clip() { # clip <name> <crf-vp9> <crf-h264> <cpu-used>
  local frames="$RAW_DIR/frames/$1"
  local list="$frames/frames.txt"
  [ -s "$list" ] || { echo "  ✗ no frames captured for $1"; exit 1; }
  local count
  count=$(grep -c "^file " "$list")

  # -f concat with per-frame durations replays the screencast in real time;
  # -r on the output resamples that to a steady rate so scrubbing and looping
  # behave in a browser.
  # The frames are JPEGs, so they decode as full-range with JPEG colour tags.
  # Left alone that produces yuvj420p with bt470bg primaries and an unknown
  # transfer — which Chromium tolerates and Safari and several players do not.
  # Convert to limited-range BT.709 and tag it explicitly, so the files say
  # what they are instead of leaving the decoder to guess.
  local VF="scale=in_range=full:out_range=tv,format=yuv420p"
  local TAG="-color_range tv -colorspace bt709 -color_primaries bt709 -color_trc bt709"

  ffmpeg -y -loglevel error -f concat -safe 0 -i "$list" \
    -r "$OUT_FPS" -vf "$VF" $TAG -c:v libvpx-vp9 -crf "$2" -b:v 0 \
    -row-mt 1 -threads 8 -deadline good -cpu-used "$4" -an \
    "$MEDIA_DIR/$1.webm"
  ffmpeg -y -loglevel error -f concat -safe 0 -i "$list" \
    -r "$OUT_FPS" -vf "$VF" $TAG -c:v libx264 -crf "$3" -preset slow -pix_fmt yuv420p \
    -profile:v main -level 4.0 -movflags +faststart -an "$MEDIA_DIR/$1.mp4"

  printf '  %-18s %s frames → %ss, webm %s / mp4 %s\n' "$1" "$count" \
    "$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$MEDIA_DIR/$1.webm" | cut -d. -f1)" \
    "$(du -h "$MEDIA_DIR/$1.webm" | cut -f1)" "$(du -h "$MEDIA_DIR/$1.mp4" | cut -f1)"
}
for THEME in "${THEMES[@]}"; do
  s="$(suffix_for "$THEME")"
  clip "hero$s"        28 19 1
  clip "walkthrough$s" 30 21 2
done

# The frontend was built at the top of this script, before any of this media
# existed, so internal/api/web/ still holds whatever was there last time. Build
# again now that the files are written, or the very next `go build` embeds the
# previous run's assets and the page serves stale media that no longer matches
# the width/height in the markup.
echo "→ Rebuilding frontend so the new media is embedded..."
( cd "$PROJECT_DIR/frontend" && npm run build --silent )

echo ""
echo "━━━ Done ━━━  total: $(du -sh "$MEDIA_DIR" | cut -f1) in frontend/public/media/"
ls -la "$MEDIA_DIR"
