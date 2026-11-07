#!/usr/bin/env bash
# Classify a dependency scan: clean, vulnerabilities_found or scan_failed.
# Usage: scan-result.sh <go|yarn> <exit-code> [yarn-output-file]
set -uo pipefail

tool="${1:-}"
code="${2:-}"
out="${3:-}"

case "$code" in
  ''|*[!0-9]*) echo scan_failed; exit 0 ;;
esac

case "$tool" in
  go)
    # govulncheck: 0 clean, 3 vulnerabilities, anything else is a tool or network failure
    case "$code" in
      0) echo clean ;;
      3) echo vulnerabilities_found ;;
      *) echo scan_failed ;;
    esac
    ;;
  yarn)
    # yarn 1 exits with a bitmask (8 high, 16 critical, lower bits set whatever --level says)
    if [ "$code" -gt 31 ]; then
      echo scan_failed
    elif [ "$code" -eq 0 ]; then
      echo clean
    elif [ $((code & 24)) -ne 0 ]; then
      echo vulnerabilities_found
    elif [ -f "$out" ] && grep -q 'Packages audited' "$out"; then
      echo clean
    else
      echo scan_failed
    fi
    ;;
  *) echo scan_failed ;;
esac
