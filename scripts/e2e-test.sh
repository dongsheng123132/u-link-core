#!/usr/bin/env bash
# End-to-end smoke test: relay + agent + controller in one process tree.
# Usage: ./scripts/e2e-test.sh
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${PORT:-18999}"
AGENT_TOKEN="e2e-agent-token"
CTL_TOKEN="e2e-controller-token"
WORK="$(mktemp -d)"
cleanup() {
  if [ -n "${AGENT_PID:-}" ]; then kill "$AGENT_PID" 2>/dev/null || true; wait "$AGENT_PID" 2>/dev/null || true; fi
  if [ -n "${RELAY_PID:-}" ]; then kill "$RELAY_PID" 2>/dev/null || true; wait "$RELAY_PID" 2>/dev/null || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "== build =="
go build -o "$WORK/server" ./cmd/server
go build -o "$WORK/agent" ./cmd/agent
go build -o "$WORK/ulink" ./cmd/ulink

echo "== start relay on :$PORT =="
AGENT_TOKEN="$AGENT_TOKEN" CONTROLLER_TOKEN="$CTL_TOKEN" REQUIRE_DEVICE_KEYS=1 \
  "$WORK/server" -port "$PORT" > "$WORK/relay.log" 2>&1 &
RELAY_PID=$!
sleep 1

echo "== start agent (enrollment code flow) =="
"$WORK/agent" --server "ws://127.0.0.1:$PORT" --token "$AGENT_TOKEN" --id e2e-device \
  --key-file "$WORK/device.key" > "$WORK/agent.log" 2>&1 &
AGENT_PID=$!

CODE=""
for _ in $(seq 1 20); do
  CODE="$(grep -o 'Enrollment code: [0-9]\{6\}' "$WORK/agent.log" | tail -1 | awk '{print $3}' || true)"
  [ -n "$CODE" ] && break
  sleep 0.5
done
if [ -z "$CODE" ]; then
  echo "FAIL: no enrollment code appeared" >&2
  cat "$WORK/agent.log" >&2
  exit 1
fi
echo "   enrollment code: $CODE"

echo "== approve enrollment =="
"$WORK/ulink" --relay "ws://127.0.0.1:$PORT" --token "$CTL_TOKEN" pair approve "$CODE" > "$WORK/approve.log" 2>&1 || {
  echo "FAIL: approve failed" >&2; cat "$WORK/approve.log" >&2; exit 1; }

echo "== wait for device online =="
for _ in $(seq 1 20); do
  "$WORK/ulink" --relay "ws://127.0.0.1:$PORT" --token "$CTL_TOKEN" devices > "$WORK/devices.log" 2>&1 || true
  grep -q "e2e-device" "$WORK/devices.log" && break
  sleep 0.5
done
grep -q "e2e-device" "$WORK/devices.log" || {
  echo "FAIL: device not online" >&2; cat "$WORK/devices.log" >&2; exit 1; }

echo "== run a command =="
"$WORK/ulink" --relay "ws://127.0.0.1:$PORT" --token "$CTL_TOKEN" --json \
  run e2e-device -- "echo u-link-e2e-ok" > "$WORK/run.log" 2>&1 || {
  echo "FAIL: run failed" >&2; cat "$WORK/run.log" >&2; exit 1; }
grep -q "u-link-e2e-ok" "$WORK/run.log" || {
  echo "FAIL: unexpected run output" >&2; cat "$WORK/run.log" >&2; exit 1; }

echo "== revoke device =="
"$WORK/ulink" --relay "ws://127.0.0.1:$PORT" --token "$CTL_TOKEN" pair revoke e2e-device > "$WORK/revoke.log" 2>&1 || {
  echo "FAIL: revoke failed" >&2; cat "$WORK/revoke.log" >&2; exit 1; }

echo "PASS: relay + enrollment + approve + exec + revoke"
