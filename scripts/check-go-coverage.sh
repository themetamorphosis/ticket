#!/bin/sh
# Fails unless total Go statement coverage in the given profile is exactly 100%.
# There is no ignore list: untestable code is restructured, not excluded.
set -eu
profile="${1:?usage: check-go-coverage.sh coverage.out}"

report="$(go tool cover -func="$profile")"
total="$(printf '%s\n' "$report" | awk '/^total:/ {print $NF}')"

if [ "$total" != "100.0%" ]; then
  echo "Go coverage gate failed: total $total (required 100.0%)"
  printf '%s\n' "$report" | awk '$NF != "100.0%" && !/^total:/'
  exit 1
fi
echo "Go coverage: $total"
