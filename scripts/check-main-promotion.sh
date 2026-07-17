#!/usr/bin/env bash

set -euo pipefail

base_sha=${1:?usage: check-main-promotion.sh <base-sha> <head-ref>}
head_ref=${2:?usage: check-main-promotion.sh <base-sha> <head-ref>}

if [[ "$head_ref" != "next" ]]; then
  echo "main accepts promotion PRs from 'next' only; got '$head_ref'" >&2
  exit 1
fi

if ! git diff --name-only "$base_sha"...HEAD | grep -Fxq CHANGELOG.md; then
  echo "promotion PRs must update CHANGELOG.md" >&2
  exit 1
fi

if ! awk '
  /^## \[Unreleased\]/ { in_unreleased = 1; found_heading = 1; next }
  in_unreleased && /^## \[/ { in_unreleased = 0 }
  in_unreleased && /^### (Added|Changed|Deprecated|Removed|Fixed|Security)$/ { found_section = 1; next }
  in_unreleased && found_section && /^- [[:alnum:]]/ { found_entry = 1 }
  END { exit !(found_heading && found_section && found_entry) }
' CHANGELOG.md; then
  echo "CHANGELOG.md [Unreleased] needs a categorized, human-readable entry" >&2
  exit 1
fi

echo "promotion policy: next -> main with a human-readable changelog entry"
