#!/usr/bin/env bash
# Regenerates internal/api from api/openapi.yaml and fails if the committed output
# differs (ADR 0005). Usable locally and in CI; needs only Go and git.
#
# Why compare against the index/HEAD with git instead of a temp copy: the generator
# writes to the real path, so `git diff` shows exactly what a contributor forgot to commit.
set -euo pipefail

cd "$(dirname "$0")/.."

go generate ./internal/api

# --intent-to-add makes a newly generated, untracked file show up in the diff too.
git add --intent-to-add internal/api
if ! git diff --exit-code -- internal/api; then
  echo "internal/api is out of date: run 'go generate ./internal/api' from backend/ and commit the result." >&2
  exit 1
fi
echo "internal/api is up to date."
