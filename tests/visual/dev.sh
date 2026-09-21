#!/bin/bash
# Build rekam, start it against a seeded local data dir, and leave it running
# for interactive poking in a real browser — the same build+seed steps
# tests/visual/run.sh uses for the Playwright suite, minus Playwright and
# minus the teardown (run.sh deletes its data dir on exit; this keeps it, so
# your session survives between runs unless you ask for a fresh one).
#
# Usage:
#   tests/visual/dev.sh            # reuse existing data if present, else seed fresh
#   tests/visual/dev.sh --fresh    # wipe and reseed first
#
# Ctrl-C stops the server. The data dir (tests/visual/.devdata) is left in
# place either way — REKAM_REGISTRY, not this script's own state, is what
# decides whether there's anything to reuse.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
DATA_DIR="$SCRIPT_DIR/.devdata"
PORT="${REKAM_DEV_PORT:-5050}"

export REKAM_ADMIN_EMAIL="admin@rekam.local"
export REKAM_ADMIN_PASSWORD="rekam-dev-password"
# Set REKAM_REGISTRY so the binary's autoLoadEnv does NOT fall back to the
# real ~/.rekam/.env — this must never touch your actual memory.
export REKAM_REGISTRY="$DATA_DIR/registry.sqlite"

NEED_SEED=0
if [ "$1" = "--fresh" ] || [ ! -f "$REKAM_REGISTRY" ]; then
  echo "→ Starting from a fresh, seeded instance..."
  rm -rf "$DATA_DIR"; mkdir -p "$DATA_DIR"
  NEED_SEED=1
else
  echo "→ Reusing existing dev data at $DATA_DIR (pass --fresh to reseed)"
fi

echo "→ Building frontend..."
( cd "$PROJECT_DIR/frontend" && npm run build --silent )
echo "→ Compiling rekam binary..."
( cd "$PROJECT_DIR" && go build -o "$DATA_DIR/rekam" ./cmd/rekam )

echo "→ Starting rekam on port $PORT..."
"$DATA_DIR/rekam" serve --registry "$REKAM_REGISTRY" --addr ":$PORT" >"$DATA_DIR/server.log" 2>&1 &
SERVER_PID=$!

cleanup() {
  echo ""
  echo "→ Stopping server (pid $SERVER_PID)... data kept at $DATA_DIR"
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

for i in $(seq 1 40); do
  curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null && break
  sleep 0.25
done
if ! curl -s -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
  echo "ERROR: server failed to start; log:"; cat "$DATA_DIR/server.log"; exit 1
fi

if [ "$NEED_SEED" = 1 ]; then
  echo "→ Seeding corpus..."
  bash "$SCRIPT_DIR/seed.sh" "http://localhost:$PORT"
fi

echo ""
echo "━━━ rekam dev instance ready ━━━"
echo "  Landing page: http://localhost:$PORT/"
echo "  Admin login:  $REKAM_ADMIN_EMAIL / $REKAM_ADMIN_PASSWORD"
echo "  Also seeded:  bob@rekam.local / password123 (member of team Acme Research)"
echo "  Data dir:     $DATA_DIR (persists — pass --fresh to wipe and reseed)"
echo ""
echo "Ctrl-C to stop."
wait "$SERVER_PID"
