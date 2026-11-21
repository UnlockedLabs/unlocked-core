#!/usr/bin/env bash
# Usage: roundtrip.sh <postgres-dsn>. Needs an empty database and the goose CLI.
# Applies each migration, rolls it back, re-applies it, then resets everything.
# Ends with goose reset, so it refuses to run against a database that already has tables.
set -euo pipefail
dsn=$1
dir=$(dirname "$0")

tables=$(psql "$dsn" -Atc "select count(*) from information_schema.tables where table_schema = 'public'")
[ "$tables" = 0 ] || { echo "refusing to run: database is not empty ($tables tables in public)" >&2; exit 1; }

g() {
  local out
  out=$(goose -dir "$dir" -table public.goose_db_version postgres "$dsn" "$@" 2>&1) || {
    echo "goose $* failed:" >&2
    echo "$out" | tail -3 | cut -c1-300 >&2
    exit 1
  }
}

latest=$(ls "$dir" | grep -E '^[0-9]{5}_.*\.sql$' | cut -c1-5 | sort | tail -1 | sed 's/^0*//')
for v in $(seq 1 "$latest"); do
  g up-to "$v"
  g down
  g up-to "$v"
done
g reset
echo "round trip ok through version $latest"
