#!/usr/bin/env bash
set -euo pipefail

assets=(
  internal/docs/assets/corpus.gob.gz
  internal/docs/assets/corpus.manifest.json
)

if ! git diff --quiet HEAD -- "${assets[@]}"; then
  echo "Commit or restore the existing docs corpus changes before refreshing." >&2
  exit 1
fi

if make docs-index && go test ./internal/docs -count=1; then
  exit 0
fi

git restore --source=HEAD --worktree -- "${assets[@]}"
echo "::warning::Docs corpus refresh or verification failed. Keeping the committed corpus."
