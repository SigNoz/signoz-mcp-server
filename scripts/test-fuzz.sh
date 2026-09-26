#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
fuzz_time=${1:-10s}
target_timeout=${2:-2m}
workers=${3:-2}
targets=(
  './internal/handler/tools:FuzzSchemaNormalization'
  './pkg/util:FuzzWebURLEnrichment'
  './pkg/types:FuzzQueryPayloadRoundTrip'
  './pkg/timeutil:FuzzExplicitTimestampUnits'
)

if ! command -v timeout >/dev/null; then
  echo 'GNU timeout is required (macOS: brew install coreutils).' >&2
  exit 2
fi
if [[ -n ${FUZZ_TARGET:-} ]]; then
  selected=()
  for entry in "${targets[@]}"; do
    if [[ ${entry#*:} == "$FUZZ_TARGET" ]]; then
      selected+=("$entry")
    fi
  done
  if [[ ${#selected[@]} == 0 ]]; then
    echo "Unknown FUZZ_TARGET: $FUZZ_TARGET" >&2
    exit 2
  fi
  targets=("${selected[@]}")
fi

mkdir -p .fuzz-artifacts
run_dir=$(mktemp -d .fuzz-artifacts/run.XXXXXX)
toolchain=$(go env GOVERSION)
echo "Fuzz logs and replay inputs: $run_dir"
{
  go version
  git rev-parse HEAD
  git status --short
  echo "fuzztime=$fuzz_time timeout=$target_timeout workers=$workers minimizetime=5s"
} > "$run_dir/environment.txt"

status=0
for entry in "${targets[@]}"; do
  package=${entry%:*}
  target=${entry#*:}
  echo "Fuzzing $package $target ($fuzz_time, $workers workers)"
  if timeout --kill-after=5s "$target_timeout" go test "$package" -run='^$' -fuzz="^${target}$" \
      -fuzztime="$fuzz_time" -fuzzminimizetime=5s -parallel="$workers" \
      -timeout="$target_timeout" 2>&1 | tee "$run_dir/$target.log"; then
    continue
  fi
  status=1
  corpus="$package/testdata/fuzz/$target"
  if [[ -d "$corpus" ]]; then
    mkdir -p "$run_dir/$package/testdata/fuzz"
    cp -R "$corpus" "$run_dir/$package/testdata/fuzz/"
    for input in "$corpus"/*; do
      [[ -f "$input" ]] || continue
      printf 'GOTOOLCHAIN=%s go test %s -run="^%s/%s$" -count=1\n' \
        "$toolchain" "$package" "$target" "$(basename "$input")" | tee -a "$run_dir/replay.txt"
    done
  else
    echo "# $target failed without a saved input; inspect its log for seed failures, build errors, or timeouts." | tee -a "$run_dir/replay.txt"
    printf 'GOTOOLCHAIN=%s go test %s -run="^%s$" -count=1\n' "$toolchain" "$package" "$target" | tee -a "$run_dir/replay.txt"
  fi
done
exit "$status"
