#!/usr/bin/env bash
# Objective gate: exit 0 = milestone passes. Does not trust the agent's self-report.
# Runtime criteria not covered here (failover behaviour etc.) are checked by you at review.
set -euo pipefail
m=${1:?usage: gate.sh M0..M7}
fail() { echo "GATE FAIL ($m): $*" >&2; exit 1; }
trap 'make down >/dev/null 2>&1 || true' EXIT

[ ! -f docs/BLOCKED.md ] || fail "docs/BLOCKED.md present"
[ -z "$(git status --porcelain)" ] || fail "uncommitted changes"
[ -s "docs/evidence/$m.md" ] || fail "missing docs/evidence/$m.md"
make test || fail "make test"

compose_ok() {
  docker compose -f deploy/compose.yaml --env-file .env.example config --format json \
    | jq -e '[.services[] | select(.mem_limit==null or .mem_reservation==null or .pids_limit==null)] | length==0' >/dev/null \
    || fail "a Compose service lacks mem_limit / mem_reservation / pids_limit"
}
keep_scorecards() { mkdir -p ".autorun/scorecards-$m" && cp -r scorecards/. ".autorun/scorecards-$m/" 2>/dev/null || true; git clean -fdq -- scorecards; }

case "$m" in
  M0|M2|M3|M6) compose_ok ;;
  M1) compose_ok; [ -s docs/memory-m1.txt ] || fail "docs/memory-m1.txt missing" ;;
  M4) compose_ok; make up; make chaos || fail "make chaos"; keep_scorecards
      jq -e '.passed==true' scorecards/baseline.json >/dev/null || fail "baseline.json not passing" ;;
  M5) compose_ok; make up; make restore-drill || fail "restore-drill"; keep_scorecards ;;
  M7) compose_ok; make up
      for i in 1 2 3; do make chaos || fail "make chaos run $i"; done
      make restore-drill || fail "restore-drill"; keep_scorecards ;;
esac
echo "GATE PASS ($m)"
