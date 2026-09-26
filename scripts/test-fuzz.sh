#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
fuzz_time=${1:-10s}
workers=${2:-2}
targets=(
  './internal/handler/tools:FuzzSchemaNormalization'
  './pkg/util:FuzzWebURLEnrichment'
  './pkg/types:FuzzQueryPayloadRoundTrip'
  './pkg/timeutil:FuzzExplicitTimestampUnits'
)

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
  echo "fuzztime=$fuzz_time workers=$workers minimizetime=5s"
} > "$run_dir/environment.txt"

status=0
for entry in "${targets[@]}"; do
  package=${entry%:*}
  target=${entry#*:}
  echo "Fuzzing $package $target ($fuzz_time, $workers workers)"
  if go test "$package" -run='^$' -fuzz="^${target}$" \
      -fuzztime="$fuzz_time" -fuzzminimizetime=5s -parallel="$workers" \
      2>&1 | tee "$run_dir/$target.log"; then
    continue
  fi
  status=1
  corpus="$package/testdata/fuzz/$target"
  saved_input=false
  # Existing corpus files may have nothing to do with this failure.
  failure_pattern="^[[:space:]]*Failing input written to testdata/fuzz/$target/([0-9a-f]+)$"
  while IFS= read -r line; do
    if [[ $line =~ $failure_pattern ]]; then
      input=${BASH_REMATCH[1]}
      [[ -f "$corpus/$input" && ! -L "$corpus/$input" ]] || continue
      mkdir -p "$run_dir/$corpus"
      cp "$corpus/$input" "$run_dir/$corpus/"
      printf 'GOTOOLCHAIN=%q go test %q -run=%q -count=1\n' \
        "$toolchain" "$package" "^$target/$input$" | tee -a "$run_dir/replay.txt"
      saved_input=true
    fi
  done < "$run_dir/$target.log"
  if [[ $saved_input == false ]]; then
    echo "# $target failed without a new saved input; inspect its log for seed, build, or runtime failures." | tee -a "$run_dir/replay.txt"
    printf 'GOTOOLCHAIN=%q go test %q -run=%q -count=1\n' "$toolchain" "$package" "^$target$" | tee -a "$run_dir/replay.txt"
  fi
done
exit "$status"
