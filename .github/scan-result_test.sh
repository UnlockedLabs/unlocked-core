#!/usr/bin/env bash
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
script="$here/scan-result.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

completed="$tmp/completed.txt"
failed="$tmp/failed.txt"
printf '188 vulnerabilities found - Packages audited: 493\n' > "$completed"
printf 'error An unexpected error occurred: "getaddrinfo ENOTFOUND"\n' > "$failed"

fails=0
check() {
  local want="$1"; shift
  local got
  got="$("$script" "$@" 2>/dev/null)"
  if [ "$got" != "$want" ]; then
    echo "FAIL: scan-result.sh $* -> '$got', want '$want'"
    fails=$((fails + 1))
  fi
}

check clean go 0
check vulnerabilities_found go 3
for code in 1 2 4 127 137 255 abc ""; do
  check scan_failed go "$code"
done

check clean yarn 0 "$completed"
check vulnerabilities_found yarn 8 "$completed"
check vulnerabilities_found yarn 16 "$completed"
check vulnerabilities_found yarn 24 "$completed"
check vulnerabilities_found yarn 30 "$completed"
check vulnerabilities_found yarn 31 "$completed"
check vulnerabilities_found yarn 8 "$failed"
check vulnerabilities_found yarn 8 "$tmp/missing.txt"
check vulnerabilities_found yarn 9 "$completed"
for code in 1 2 4 6 7; do
  check clean yarn "$code" "$completed"
  check scan_failed yarn "$code" "$failed"
  check scan_failed yarn "$code" "$tmp/missing.txt"
done
for code in 32 127 137 255 abc ""; do
  check scan_failed yarn "$code" "$completed"
done
check scan_failed yarn 1
check scan_failed rust 0

if [ "$fails" -ne 0 ]; then
  echo "$fails failed"
  exit 1
fi
echo "all passed"
