#!/usr/bin/env bash
# Manual failover drill (M2): SIGKILL the current primary, time the promotion of the standby,
# start the old primary again and time its rejoin. Fails if two primaries are ever seen.
# The measured experiment with load is E4 (`blast run`, M4); this is the by-hand check.
set -euo pipefail

project=${COMPOSE_PROJECT:-blastradius}
compose=(docker compose -f deploy/compose.yaml --env-file "${ENV_FILE:-.env}")
max_primaries=0

now() { date +%s.%N; }
since() { awk -v a="$1" -v b="$(now)" 'BEGIN { printf "%.1f", b - a }'; }

# wait_for <awk pattern on timestamped status output> <timeout seconds>
# Also records the highest primary count seen while polling.
wait_for() {
  local deadline=$(($(date +%s) + $2)) out n
  while [ "$(date +%s)" -lt "$deadline" ]; do
    out=$(bash scripts/pgstatus.sh | awk -v ts="$(date -u +%H:%M:%S)" '{ print ts, $0 }' || true)
    echo "$out"
    n=$(awk -F= '/primaries=/ { print $2 }' <<<"$out")
    [ "$n" -le "$max_primaries" ] || max_primaries=$n
    if awk "$1 { found = 1 } END { exit !found }" <<<"$out"; then return 0; fi
    sleep 1
  done
  echo "timeout after $2 s waiting for: $1" >&2
  return 1
}

old=$(bash scripts/pgstatus.sh | awk '$2 == "primary" { print $1 }')
[ "$old" = pg1 ] && new=pg2 || new=pg1
echo "== primary is $old; killing it with SIGKILL"
t_kill=$(now)
"${compose[@]}" kill -s SIGKILL "$old"

wait_for "\$2 ~ /$new/ && \$3 == \"primary\"" 90
promote_s=$(since "$t_kill")
echo "== $new promoted ${promote_s} s after the kill"
echo "== $old container state after kill: $(docker inspect -f '{{.State.Status}} restarts={{.RestartCount}}' "$project-$old-1")"

echo "== starting $old again"
t_start=$(now)
"${compose[@]}" start "$old"
wait_for "\$2 ~ /$old/ && \$3 == \"standby\" && /receiver=streaming/" 180
rejoin_s=$(since "$t_start")

echo "== result: promote_seconds=$promote_s rejoin_seconds=$rejoin_s max_primaries_seen=$max_primaries"
[ "$max_primaries" -le 1 ]
