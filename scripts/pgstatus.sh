#!/usr/bin/env bash
# SQL probe (as the `probe` role, pg_monitor) for each Postgres node: role and replica lag.
# Prints one line per node and exits non-zero unless exactly one primary answers.
set -euo pipefail

project=${COMPOSE_PROJECT:-blastradius}
primaries=0

probe() { docker exec "$project-$1-1" psql -XAtU probe -d blastradius -c "$2" 2>/dev/null; }

for node in pg1 pg2; do
  case "$(probe "$node" 'SELECT pg_is_in_recovery()' || true)" in
    f)
      primaries=$((primaries + 1))
      echo "$node primary  standbys(name,state,lag_bytes,replay_lag_s): $(probe "$node" \
        "SELECT COALESCE(string_agg(format('%s,%s,%s,%s', application_name, state,
                  pg_wal_lsn_diff(pg_current_wal_lsn(), replay_lsn),
                  COALESCE(EXTRACT(EPOCH FROM replay_lag), 0)), ' '), 'none')
           FROM pg_stat_replication")"
      ;;
    t)
      echo "$node standby  receiver=$(probe "$node" \
        "SELECT COALESCE((SELECT status FROM pg_stat_wal_receiver), 'down')") lag_seconds=$(probe "$node" \
        "SELECT CASE WHEN pg_last_wal_receive_lsn() = pg_last_wal_replay_lsn() THEN 0
                     ELSE COALESCE(EXTRACT(EPOCH FROM now() - pg_last_xact_replay_timestamp()), 0) END")"
      ;;
    *) echo "$node unreachable" ;;
  esac
done
echo "primaries=$primaries"
[ "$primaries" -eq 1 ]
