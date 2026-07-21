#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
MCP_PROTOCOL_PORT=${MCP_PROTOCOL_PORT:-18080}
MCP_PROTOCOL_HOST=127.0.0.1
MCP_URL="http://${MCP_PROTOCOL_HOST}:${MCP_PROTOCOL_PORT}/mcp"
READY_URL="http://${MCP_PROTOCOL_HOST}:${MCP_PROTOCOL_PORT}/readyz"
COMMAND_TIMEOUT_SECONDS=60
READINESS_TIMEOUT_SECONDS=30
INSPECTOR_VERSION=1.0.0

PROTOCOL_TMP_DIR=""
SERVER_PID=""
CURRENT_PHASE="setup"
CURRENT_STDOUT=""
CURRENT_STDERR=""
SERVER_LOG=""

cleanup() {
  local status=$?
  trap - EXIT INT TERM

  if [[ $status -ne 0 ]]; then
    printf 'MCP protocol check failed during: %s\n' "$CURRENT_PHASE" >&2
    if [[ -n "$CURRENT_STDOUT" && -s "$CURRENT_STDOUT" ]]; then
      printf '%s\n' '--- command stdout ---' >&2
      sed -n '1,240p' "$CURRENT_STDOUT" >&2
    fi
    if [[ -n "$CURRENT_STDERR" && -s "$CURRENT_STDERR" ]]; then
      printf '%s\n' '--- command stderr ---' >&2
      sed -n '1,240p' "$CURRENT_STDERR" >&2
    fi
    if [[ -n "$SERVER_LOG" && -s "$SERVER_LOG" ]]; then
      printf '%s\n' '--- server log (last 120 lines) ---' >&2
      tail -n 120 "$SERVER_LOG" >&2
    fi
  fi

  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    for _ in {1..5}; do
      if ! kill -0 "$SERVER_PID" 2>/dev/null; then
        break
      fi
      sleep 1
    done
    if kill -0 "$SERVER_PID" 2>/dev/null; then
      kill -KILL "$SERVER_PID" 2>/dev/null || true
    fi
    wait "$SERVER_PID" 2>/dev/null || true
  fi

  if [[ -n "$PROTOCOL_TMP_DIR" && -d "$PROTOCOL_TMP_DIR" ]]; then
    rm -r "$PROTOCOL_TMP_DIR"
  fi

  exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT TERM

fail() {
  printf '%s\n' "$1" >"$CURRENT_STDERR"
  return 1
}

run_timed() {
  local phase=$1
  local stdout_file=$2
  local stderr_file=$3
  shift 3

  CURRENT_PHASE=$phase
  CURRENT_STDOUT=$stdout_file
  CURRENT_STDERR=$stderr_file
  timeout --signal=TERM --kill-after=5s "${COMMAND_TIMEOUT_SECONDS}s" "$@" >"$stdout_file" 2>"$stderr_file"
}

run_inspector() {
  local phase=$1
  local output_file=$2
  shift 2

  local inspector_stderr="${output_file}.stderr"
  CURRENT_PHASE=$phase
  CURRENT_STDOUT=$output_file
  CURRENT_STDERR=$inspector_stderr

  # Inspector 1.0.0 resolves its package metadata from this working directory.
  (
    cd "$ROOT_DIR/tools/mcp-ci/node_modules/@modelcontextprotocol/inspector-cli/build"
    timeout --signal=TERM --kill-after=5s "${COMMAND_TIMEOUT_SECONDS}s" \
      ./cli.js --cli --transport http "$MCP_URL" "$@"
  ) >"$output_file" 2>"$inspector_stderr"
}

assert_json() {
  local phase=$1
  local json_file=$2
  local filter=$3

  CURRENT_PHASE=$phase
  CURRENT_STDOUT=$json_file
  CURRENT_STDERR="${json_file}.assert.stderr"
  if ! jq -e "$filter" "$json_file" >/dev/null 2>"$CURRENT_STDERR"; then
    printf 'JSON assertion failed: %s\n' "$filter" >>"$CURRENT_STDERR"
    return 1
  fi
}

for dependency in curl go jq node timeout; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Required command is unavailable: %s\n' "$dependency" >&2
    exit 1
  fi
done

if [[ ! "$MCP_PROTOCOL_PORT" =~ ^[0-9]+$ ]] || ((MCP_PROTOCOL_PORT < 1 || MCP_PROTOCOL_PORT > 65535)); then
  printf 'MCP_PROTOCOL_PORT must be an integer between 1 and 65535\n' >&2
  exit 1
fi

PROTOCOL_TMP_DIR=$(mktemp -d)
SERVER_LOG="$PROTOCOL_TMP_DIR/server.log"
CURRENT_STDOUT="$PROTOCOL_TMP_DIR/setup.stdout"
CURRENT_STDERR="$PROTOCOL_TMP_DIR/setup.stderr"

if ! node -e '
  const net = require("node:net");
  const server = net.createServer();
  server.once("error", () => process.exit(1));
  server.listen({host: process.argv[1], port: Number(process.argv[2])}, () => server.close());
' "$MCP_PROTOCOL_HOST" "$MCP_PROTOCOL_PORT" >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"; then
  fail "Port ${MCP_PROTOCOL_PORT} is already occupied"
fi

node -e '
  const current = process.versions.node.split(".").map(Number);
  const minimum = [22, 7, 5];
  const supported = current.some((value, index) => value > minimum[index] && current.slice(0, index).every((part, i) => part === minimum[i])) || current.every((value, index) => value === minimum[index]);
  if (!supported) {
    console.error("Node >=" + minimum.join(".") + " is required; found " + process.versions.node);
    process.exit(1);
  }
' >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"

installed_inspector_version=$(node -p "require('$ROOT_DIR/tools/mcp-ci/node_modules/@modelcontextprotocol/inspector-cli/package.json').version")
if [[ "$installed_inspector_version" != "$INSPECTOR_VERSION" ]]; then
  fail "Inspector CLI ${INSPECTOR_VERSION} is required; found ${installed_inspector_version}"
fi

CURRENT_PHASE="build server"
go build -o "$PROTOCOL_TMP_DIR/signoz-mcp-server" ./cmd/server >"$CURRENT_STDOUT" 2>"$CURRENT_STDERR"

env \
  TRANSPORT_MODE=http \
  MCP_SERVER_HOST="$MCP_PROTOCOL_HOST" \
  MCP_SERVER_PORT="$MCP_PROTOCOL_PORT" \
  SIGNOZ_URL=https://example.invalid \
  SIGNOZ_API_KEY=protocol-test-key \
  OAUTH_ENABLED=false \
  ANALYTICS_ENABLED=false \
  OTEL_TRACES_EXPORTER=none \
  OTEL_METRICS_EXPORTER=none \
  "$PROTOCOL_TMP_DIR/signoz-mcp-server" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

CURRENT_PHASE="server readiness"
CURRENT_STDOUT="$PROTOCOL_TMP_DIR/readiness.stdout"
CURRENT_STDERR="$PROTOCOL_TMP_DIR/readiness.stderr"
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

initialize_body="$PROTOCOL_TMP_DIR/initialize.json"
initialize_headers="$PROTOCOL_TMP_DIR/initialize.headers"
initialize_status="$PROTOCOL_TMP_DIR/initialize.status"
run_timed "initialize request" "$initialize_status" "$PROTOCOL_TMP_DIR/initialize.stderr" \
  curl --silent --show-error --max-time "$COMMAND_TIMEOUT_SECONDS" \
  --output "$initialize_body" --dump-header "$initialize_headers" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' --header 'Accept: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"signoz-protocol-ci","version":"1.0.0"}}}' \
  "$MCP_URL"

CURRENT_PHASE="initialize HTTP contract"
CURRENT_STDOUT=$initialize_body
CURRENT_STDERR="$PROTOCOL_TMP_DIR/initialize-contract.stderr"
if [[ $(<"$initialize_status") != "200" ]]; then
  fail "Initialize returned HTTP $(<"$initialize_status"), expected 200"
fi
if ! grep -Eiq '^content-type:[[:space:]]*application/json([[:space:]]*;|[[:space:]]*\r?$)' "$initialize_headers"; then
  fail "Initialize response did not use an application/json content type"
fi
if grep -Eiq '^mcp-session-id:' "$initialize_headers"; then
  fail "Stateless initialize unexpectedly returned Mcp-Session-Id"
fi
assert_json "initialize JSON contract" "$initialize_body" '
  .jsonrpc == "2.0" and
  .id == 1 and
  .result.protocolVersion == "2025-11-25" and
  .result.serverInfo.name == "SigNozMCP" and
  (.result.serverInfo.version | type == "string" and length > 0) and
  (.result.instructions | type == "string" and length > 0) and
  (.result.capabilities | has("tools") and has("resources") and has("prompts") and has("logging"))
'

tools_json="$PROTOCOL_TMP_DIR/tools.json"
run_inspector "Inspector tools/list" "$tools_json" --method tools/list
assert_json "tools/list contract" "$tools_json" '
  (.tools | type == "array" and length > 0) and
  all(.tools[]; (.name | type == "string" and length > 0)) and
  any(.tools[]; .name == "signoz_search_docs")
'

resources_json="$PROTOCOL_TMP_DIR/resources.json"
run_inspector "Inspector resources/list" "$resources_json" --method resources/list
assert_json "resources/list contract" "$resources_json" '
  (.resources | type == "array" and length > 0) and
  all(.resources[]; (.name | type == "string" and length > 0) and (.uri | type == "string" and length > 0))
'

templates_json="$PROTOCOL_TMP_DIR/resource-templates.json"
run_inspector "Inspector resources/templates/list" "$templates_json" --method resources/templates/list
assert_json "resources/templates/list contract" "$templates_json" '
  (.resourceTemplates | type == "array" and length > 0) and
  all(.resourceTemplates[]; (.name | type == "string" and length > 0) and (.uriTemplate | type == "string" and length > 0))
'

prompts_json="$PROTOCOL_TMP_DIR/prompts.json"
run_inspector "Inspector prompts/list" "$prompts_json" --method prompts/list
assert_json "prompts/list contract" "$prompts_json" '
  (.prompts | type == "array" and length > 0) and
  all(.prompts[]; (.name | type == "string" and length > 0))
'

tool_call_json="$PROTOCOL_TMP_DIR/tool-call.json"
run_inspector "Inspector tools/call" "$tool_call_json" \
  --method tools/call --tool-name signoz_search_docs \
  --tool-arg searchText=docker \
  --tool-arg 'searchContext=How do I send Docker logs to SigNoz?' \
  --tool-arg limit=1
assert_json "tools/call contract" "$tool_call_json" '
  ((.isError // false) == false) and
  (.structuredContent.results | type == "array" and length > 0) and
  any(.content[]; .type == "text" and (.text | type == "string" and length > 0))
'

resource_read_json="$PROTOCOL_TMP_DIR/resource-read.json"
run_inspector "Inspector resources/read" "$resource_read_json" \
  --method resources/read --uri signoz://docs/sitemap
assert_json "resources/read contract" "$resource_read_json" '
  (.contents | type == "array" and length > 0) and
  any(.contents[]; ((.text // .blob // "") | type == "string" and length > 0))
'

prompt_get_json="$PROTOCOL_TMP_DIR/prompt-get.json"
run_inspector "Inspector prompts/get" "$prompt_get_json" \
  --method prompts/get --prompt-name debug_service_errors \
  --prompt-args service=checkout --prompt-args timeRange=1h
assert_json "prompts/get contract" "$prompt_get_json" '
  (.messages | type == "array" and length > 0) and
  any(.messages[]; .content.type == "text" and (.content.text | type == "string" and length > 0))
'

logging_json="$PROTOCOL_TMP_DIR/logging.json"
run_inspector "Inspector logging/setLevel" "$logging_json" \
  --method logging/setLevel --log-level info
assert_json "logging/setLevel contract" "$logging_json" 'type == "object"'

printf '%s\n' 'MCP protocol check passed: initialize, tools, resources, templates, prompts, and logging'
