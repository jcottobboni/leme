#!/usr/bin/env bash
# Writes the license text of every Go module linked into the engine binary (from its build info),
# so the packaged app carries the notices its dependencies require.
# Usage: scripts/go-licenses.sh <engine binary> <output file>
set -euo pipefail
bin=$1 out=$2
GO=${GO:-go}
modcache=$("$GO" env GOMODCACHE)

{
  echo "The Leme engine links the following Go modules, unmodified. Their licenses follow."
  "$GO" version -m "$bin" | awk '$1 == "dep" { print $2, $3 } $1 == "=>" { print $2, $3 }' | sort -u |
    while read -r mod ver; do
      # The module cache escapes upper-case letters as "!" + lower-case.
      dir="$modcache/$(printf '%s' "$mod" | sed 's/[A-Z]/!\L&/g')@$ver"
      files=$(find "$dir" -maxdepth 1 -type f -iregex '.*/\(licen[cs]e\|copying\|notice\)\([-_.][a-z0-9._-]*\)?' 2>/dev/null | sort)
      if [ -z "$files" ]; then
        echo "no license file for $mod@$ver ($dir)" >&2
        exit 1
      fi
      printf '\n%s\n\n%s@%s\n' "$(printf '=%.0s' {1..78})" "$mod" "$ver"
      for f in $files; do printf '\n'; cat "$f"; done
    done
} > "$out"
echo "$(grep -c '^=\{78\}$' "$out") modules -> $out"
