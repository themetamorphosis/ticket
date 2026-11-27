#!/bin/sh
# The coverage gate allows no escape hatches in our code (vendored third-party
# code is exempt): no coverage-ignore comments,
# no inline lint disables and no Go build tags that hide files from tests.
set -eu
cd "$(dirname "$0")/.."

pattern='(v8|c8|istanbul) ignore|eslint-disable|@ts-(ignore|nocheck)|//nolint|//go:build ignore|// \+build ignore'
matches="$(grep -rnE "$pattern" backend web api \
  --include='*.go' --include='*.js' --include='*.jsx' --include='*.mjs' --include='*.ts' --include='*.tsx' \
  --exclude-dir=node_modules --exclude-dir=vendor --exclude-dir=.next --exclude-dir=out --exclude-dir=coverage || true)"

if [ -n "$matches" ]; then
  echo "Ignore pragmas are not allowed:"
  echo "$matches"
  exit 1
fi
echo "No ignore pragmas."
