#!/bin/bash
# Rekam visual tests. Builds the app, runs it on the host against a fresh seeded
# data dir, then drives it with Playwright *inside the official Playwright Docker
# image* (--network host) so the browser binaries come from the image — the host
# (Fedora) doesn't need working Playwright browsers, only Docker.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
TIMESTAMP=$(date +"%Y-%m-%d_%H-%M-%S")
RESULTS_DIR="$SCRIPT_DIR/results/$TIMESTAMP"
DATA_DIR="$SCRIPT_DIR/.rundata"
PORT=3010
# The marketing site is a separate static build served from the origin root in
# production (Cloudflare in front of the app). Here it gets its own port and
# its own trivial static server, so the landing and legal tests exercise the
# same two-origin shape the real deployment has rather than a fiction where
# the app serves its own advertising.
MARKETING_PORT=3011
DOCKER_IMAGE="mcr.microsoft.com/playwright:v1.53.0-noble"
# Keep in lock-step with the Docker image tag: the mounted test runner must match
# the browser build baked into the image.
PLAYWRIGHT_VERSION=1.53.0

export REKAM_TEST_PORT="$PORT"
export REKAM_MARKETING_PORT="$MARKETING_PORT"
export REKAM_ADMIN_EMAIL="admin@rekam.local"
# Must be >= 8 chars — the binary rejects shorter passwords, so the bootstrap
# admin would silently fail to be created (and every seeded write would 401).
export REKAM_ADMIN_PASSWORD="rekam-visual-test"
# Set REKAM_REGISTRY so the binary's autoLoadEnv does NOT fall back to the real
# ~/.rekam/.env — these tests must never touch the developer's own memory.
export REKAM_REGISTRY="$DATA_DIR/registry.sqlite"

mkdir -p "$RESULTS_DIR"
rm -rf "$DATA_DIR"; mkdir -p "$DATA_DIR"

echo "━━━ Rekam Visual Tests ━━━"
echo "  Results: $RESULTS_DIR"
echo "  Data:    $DATA_DIR (fresh)"
echo ""

# 1. Build
echo "→ Building frontend..."
( cd "$PROJECT_DIR/frontend" && npm run build --silent )
echo "→ Building marketing site..."
# Full split (edge/wrangler.toml): the marketing build bakes in an absolute
# app origin for login/signup/docs links, since there is no proxy for a
# relative link to resolve against anymore. Point that at this run's own
# local app instance instead of the real app.rekam.net.
( cd "$PROJECT_DIR/frontend" && REKAM_APP_ORIGIN="http://localhost:$PORT" npm run build:marketing --silent )
echo "→ Compiling rekam binary..."
( cd "$PROJECT_DIR" && go build -o "$DATA_DIR/rekam" ./cmd/rekam )

# 2. Start server against the fresh data dir (bootstraps admin@rekam.local/test).
echo "→ Starting rekam on port $PORT..."
"$DATA_DIR/rekam" serve --registry "$REKAM_REGISTRY" --addr ":$PORT" >"$DATA_DIR/server.log" 2>&1 &
SERVER_PID=$!

# Edge host for the marketing build. It maps /terms to terms.html the way
# the real rekam-edge Worker's static assets do — a pure static server, no
# app routing: the marketing build's own links already point straight at the
# app origin above, matching the real deployment's full split.
echo "→ Serving marketing site on port $MARKETING_PORT..."
python3 "$SCRIPT_DIR/serve-marketing.py" "$PROJECT_DIR/dist/marketing" "$MARKETING_PORT" \
  >"$DATA_DIR/marketing.log" 2>&1 &
MARKETING_PID=$!

cleanup() {
  echo "→ Stopping servers (pids $SERVER_PID $MARKETING_PID)..."
  kill "$SERVER_PID" "$MARKETING_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  wait "$MARKETING_PID" 2>/dev/null || true
  rm -rf "$DATA_DIR" 2>/dev/null || true
}
trap cleanup EXIT

for i in $(seq 1 40); do
  curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null && break
  sleep 0.25
done
if ! curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
  echo "ERROR: server failed to start; log:"; cat "$DATA_DIR/server.log"; exit 1
fi
echo "  Server ready."

for i in $(seq 1 40); do
  curl -s -o /dev/null "http://localhost:$MARKETING_PORT/" 2>/dev/null && break
  sleep 0.25
done
if ! curl -s -o /dev/null "http://localhost:$MARKETING_PORT/" 2>/dev/null; then
  echo "ERROR: marketing site failed to start; log:"; cat "$DATA_DIR/marketing.log"; exit 1
fi
echo "  Marketing site ready."

# 3. Seed the corpus over the API.
echo "→ Seeding corpus..."
bash "$SCRIPT_DIR/seed.sh" "http://localhost:$PORT"
echo ""

# 4. Ensure the Playwright test runner is installed locally (once). We mount this
# into the container and run it directly. Browsers come from the image; the test
# runner comes from here. Installing a second copy inside the container makes
# Playwright see two @playwright/test instances and silently collect zero tests.
if [ ! -x "$SCRIPT_DIR/node_modules/.bin/playwright" ]; then
  echo "→ Installing Playwright test runner (@playwright/test@$PLAYWRIGHT_VERSION)..."
  ( cd "$SCRIPT_DIR" && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm install --silent --no-save "@playwright/test@$PLAYWRIGHT_VERSION" )
fi

# 5. Run tests in Docker using the mounted runner + the image's browsers.
echo "→ Running visual tests in Docker..."
set +e
docker run --rm \
  --network host \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -e RESULTS_DIR=/output \
  -e REKAM_TEST_PORT="$PORT" \
  -e REKAM_MARKETING_PORT="$MARKETING_PORT" \
  -e REKAM_ADMIN_EMAIL="$REKAM_ADMIN_EMAIL" \
  -e REKAM_ADMIN_PASSWORD="$REKAM_ADMIN_PASSWORD" \
  -e REKAM_SLOWMO="${REKAM_SLOWMO:-}" \
  -v "$SCRIPT_DIR:/tests" \
  -v "$RESULTS_DIR:/output" \
  -w /tests \
  "$DOCKER_IMAGE" \
  bash -c "./node_modules/.bin/playwright test --config /tests/playwright.config.js"
EXIT_CODE=$?
set -e

echo ""
echo "━━━ Done ━━━"
echo "  Report: $RESULTS_DIR/index.html"
echo ""
exit $EXIT_CODE
