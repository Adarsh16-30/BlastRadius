#!/usr/bin/env bash
# Staggered-restart drill (M2): after a failover the old primary never rejoined, both nodes go
# down, and the STALE node boots first. The node with the newer timeline must stay primary, the
# stale one must yield and rejoin, and a row written after the failover must survive.
set -euo pipefail

project=${COMPOSE_PROJECT:-blastradius}
compose=(docker compose -f deploy/compose.yaml --env-file "${ENV_FILE:-.env}")
writer="stale-drill-$(date +%s)"

status() { bash scripts/pgstatus.sh || true; }
# wait_for <grep pattern on pgstatus output> <timeout seconds>
wait_for() {
  local deadline=$(($(date +%s) + $2)) out
  while [ "$(date +%s)" -lt "$deadline" ]; do
    out=$(status)
    if grep -q "$1" <<<"$out"; then
      echo "$out"
      return 0
    fi
    sleep 2
  done
  echo "timeout after $2 s waiting for: $1" >&2
  status >&2
  return 1
}

old=$(status | awk '$2 == "primary" { print $1 }')
[ "$old" = pg1 ] && new=pg2 || new=pg1
echo "== primary is $old; killing it and leaving it down"
"${compose[@]}" kill -s SIGKILL "$old"
wait_for "^$new primary" 90

echo "== writing a row through the API on the new primary $new"
tries=0
until curl -sf -m 5 -X POST -H 'Content-Type: application/json' \
  -d "{\"writer_id\":\"$writer\",\"seq\":1}" -o /dev/null http://127.0.0.1:8080/v1/seq; do
  tries=$((tries + 1))
  [ "$tries" -lt 30 ] || { echo "write never succeeded" >&2; exit 1; }
  sleep 2
done

echo "== stopping $new, then starting only the stale $old"
"${compose[@]}" stop "$new"
"${compose[@]}" start "$old"
wait_for "^$old primary" 90

echo "== starting $new (newer timeline); $old must yield and rejoin"
"${compose[@]}" start "$new"
wait_for "^$old standby  receiver=streaming" 180
wait_for "^$new primary" 30 >/dev/null

echo "== entrypoint decisions"
docker logs "$project-$new-1" 2>&1 | grep 'entrypoint\[' | tail -n 3
docker logs "$project-$old-1" 2>&1 | grep 'entrypoint\[' | tail -n 6

got=$(curl -sf -m 5 "http://127.0.0.1:8080/v1/seq/$writer")
echo "== row written after the failover: $got"
grep -q '1' <<<"$got" || { echo "FAIL: the row was lost" >&2; exit 1; }
echo "== result: PASS ($new kept the primary role, $old rejoined, row survived)"
