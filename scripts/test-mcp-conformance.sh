#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
CONFORMANCE_VERSION=0.2.0-alpha.11
CONFORMANCE_PACKAGE_DIR="$ROOT_DIR/tools/mcp-ci/node_modules/@modelcontextprotocol/conformance"
CONFORMANCE_CLI="$CONFORMANCE_PACKAGE_DIR/dist/index.js"
MCP_CONFORMANCE_HOST=127.0.0.1
MCP_CONFORMANCE_PORT=${MCP_CONFORMANCE_PORT:-18081}
MCP_URL="http://${MCP_CONFORMANCE_HOST}:${MCP_CONFORMANCE_PORT}/mcp"
READY_URL="http://${MCP_CONFORMANCE_HOST}:${MCP_CONFORMANCE_PORT}/readyz"
READINESS_TIMEOUT_SECONDS=30
SCENARIO_TIMEOUT_SECONDS=30

CONFORMANCE_TMP_DIR=""
SERVER_PID=""
SERVER_LOG=""
CURRENT_PHASE="setup"
CURRENT_STDOUT=""
CURRENT_STDERR=""
CURRENT_RESULTS_DIR=""

print_bounded_file() {
  local label=$1
  local file=$2

  if [[ -s "$file" ]]; then
    printf '%s\n' "--- ${label} (first 200 lines) ---" >&2
    sed -n '1,200p' "$file" >&2
  fi
}

stop_server_for_cleanup() {
  if [[ -z "$SERVER_PID" ]]; then
    return
  fi

  if kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    for _ in {1..25}; do
      if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        break
      fi
      sleep 0.2
    done
    if kill -0 "$SERVER_PID" 2>/dev/null; then
      kill -KILL "$SERVER_PID" 2>/dev/null || true
    fi
  fi
  wait "$SERVER_PID" 2>/dev/null || true
  SERVER_PID=""
}

cleanup() {
  local status=$?
  local checks_file=""
  set +e
  trap - EXIT INT TERM

  if [[ $status -ne 0 ]]; then
    printf 'MCP conformance check failed during: %s\n' "$CURRENT_PHASE" >&2
    if [[ -n "$CURRENT_STDOUT" ]]; then
      print_bounded_file 'command stdout' "$CURRENT_STDOUT"
    fi
    if [[ -n "$CURRENT_STDERR" ]]; then
      print_bounded_file 'command stderr' "$CURRENT_STDERR"
    fi
    if [[ -n "$CURRENT_RESULTS_DIR" && -d "$CURRENT_RESULTS_DIR" ]]; then
      checks_file=$(find "$CURRENT_RESULTS_DIR" -name checks.json -type f -print -quit)
      if [[ -n "$checks_file" ]]; then
        print_bounded_file 'conformance checks' "$checks_file"
      fi
    fi
    if [[ -n "$SERVER_LOG" && -s "$SERVER_LOG" ]]; then
      printf '%s\n' '--- server log (last 120 lines) ---' >&2
      tail -n 120 "$SERVER_LOG" >&2
    fi
  fi

  stop_server_for_cleanup
  if [[ -n "$CONFORMANCE_TMP_DIR" && -d "$CONFORMANCE_TMP_DIR" ]]; then
    rm -r "$CONFORMANCE_TMP_DIR"
  fi

  exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT TERM

fail() {
  printf '%s\n' "$1" >"$CURRENT_STDERR"
  return 1
}

run_scenario() {
  local spec_version=$1
  local scenario=$2
  local slug="${spec_version}-${scenario}"

  CURRENT_PHASE="${spec_version} ${scenario}"
  CURRENT_STDOUT="$CONFORMANCE_TMP_DIR/${slug}.stdout"
  CURRENT_STDERR="$CONFORMANCE_TMP_DIR/${slug}.stderr"
  CURRENT_RESULTS_DIR="$CONFORMANCE_TMP_DIR/${slug}-results"

  timeout --signal=TERM --kill-after=5s "${SCENARIO_TIMEOUT_SECONDS}s" \
    node "$CONFORMANCE_CLI" server \
    --url "$MCP_URL" \
    --scenario "$scenario" \
    --spec-version "$spec_version" \
    --output-dir "$CURRENT_RESULTS_DIR" \
    >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"

  if grep -q '^SKIPPED:' "$CURRENT_STDOUT"; then
    fail "Scenario ${scenario} was skipped at ${spec_version}; every selected scenario must run"
  fi

  printf 'passed: %s / %s\n' "$spec_version" "$scenario"
}

for dependency in curl go node timeout; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Required command is unavailable: %s\n' "$dependency" >&2
    exit 1
  fi
done

if [[ ! "$MCP_CONFORMANCE_PORT" =~ ^[0-9]+$ ]] || ((MCP_CONFORMANCE_PORT < 1 || MCP_CONFORMANCE_PORT > 65535)); then
  printf 'MCP_CONFORMANCE_PORT must be an integer between 1 and 65535\n' >&2
  exit 1
fi

CONFORMANCE_TMP_DIR=$(mktemp -d)
SERVER_LOG="$CONFORMANCE_TMP_DIR/server.log"
CURRENT_STDOUT="$CONFORMANCE_TMP_DIR/setup.stdout"
CURRENT_STDERR="$CONFORMANCE_TMP_DIR/setup.stderr"

if [[ ! -f "$CONFORMANCE_PACKAGE_DIR/package.json" || ! -f "$CONFORMANCE_CLI" ]]; then
  fail "MCP conformance runner is not installed; run npm ci in tools/mcp-ci"
fi

installed_runner_version=$(node "$CONFORMANCE_CLI" --version)
if [[ "$installed_runner_version" != "$CONFORMANCE_VERSION" ]]; then
  fail "MCP conformance runner ${CONFORMANCE_VERSION} is required; found ${installed_runner_version}"
fi

if ! node -e '
  const net = require("node:net");
  const server = net.createServer();
  server.once("error", () => process.exit(1));
  server.listen({ host: process.argv[1], port: Number(process.argv[2]) }, () => server.close());
' "$MCP_CONFORMANCE_HOST" "$MCP_CONFORMANCE_PORT" >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"; then
  fail "Port ${MCP_CONFORMANCE_PORT} is already occupied on ${MCP_CONFORMANCE_HOST}"
fi

CURRENT_PHASE="build production server"
go build -o "$CONFORMANCE_TMP_DIR/signoz-mcp-server" ./cmd/server >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"

env \
  TRANSPORT_MODE=http \
  MCP_SERVER_HOST="$MCP_CONFORMANCE_HOST" \
  MCP_SERVER_PORT="$MCP_CONFORMANCE_PORT" \
  SIGNOZ_URL=https://example.invalid \
  SIGNOZ_API_KEY=conformance-test-key \
  OAUTH_ENABLED=false \
  ANALYTICS_ENABLED=false \
  OTEL_TRACES_EXPORTER=none \
  OTEL_METRICS_EXPORTER=none \
  "$CONFORMANCE_TMP_DIR/signoz-mcp-server" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

CURRENT_PHASE="server readiness"
CURRENT_STDOUT="$CONFORMANCE_TMP_DIR/readiness.stdout"
CURRENT_STDERR="$CONFORMANCE_TMP_DIR/readiness.stderr"
readiness_deadline=$((SECONDS + READINESS_TIMEOUT_SECONDS))
until curl --fail --silent --show-error --max-time 2 "$READY_URL" >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"; do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "Server exited before becoming ready"
  fi
  if ((SECONDS >= readiness_deadline)); then
    fail "Server did not become ready within ${READINESS_TIMEOUT_SECONDS} seconds"
  fi
  sleep 1
done

legacy_scenarios=(
  server-initialize
  ping
  tools-list
  resources-list
  prompts-list
  dns-rebinding-protection
)
modern_scenarios=(
  tools-list
  resources-list
  sep-2164-resource-not-found
  prompts-list
  dns-rebinding-protection
  caching
)

for scenario in "${legacy_scenarios[@]}"; do
  run_scenario 2025-11-25 "$scenario"
done
for scenario in "${modern_scenarios[@]}"; do
  run_scenario 2026-07-28 "$scenario"
done

CURRENT_PHASE="clean server shutdown"
CURRENT_STDOUT="$CONFORMANCE_TMP_DIR/shutdown.stdout"
CURRENT_STDERR="$CONFORMANCE_TMP_DIR/shutdown.stderr"
kill -TERM "$SERVER_PID"
shutdown_deadline=$((SECONDS + 5))
while kill -0 "$SERVER_PID" 2>/dev/null && ((SECONDS < shutdown_deadline)); do
  sleep 0.2
done
if kill -0 "$SERVER_PID" 2>/dev/null; then
  fail "Server did not stop within 5 seconds of SIGTERM"
fi
if ! wait "$SERVER_PID"; then
  SERVER_PID=""
  fail "Server exited unsuccessfully after SIGTERM"
fi
SERVER_PID=""

printf '%s\n' 'MCP conformance check passed: 6 legacy and 6 modern scenarios against the production server'
