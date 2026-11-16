#!/usr/bin/env bash
# Entrypoint for both Postgres nodes (PRD 6.4). Stays PID 1 so repmgr can stop and start
# postgres with pg_ctl (rewind, rejoin) without ending the container.
set -euo pipefail

for v in NODE_ID NODE_NAME PEER_HOST PG_ROLE PGDATA PGPASSFILE \
  POSTGRES_PASSWORD REPMGR_PASSWORD APP_PASSWORD MIGRATOR_PASSWORD PROBE_PASSWORD; do
  [ -n "${!v:-}" ] || { echo "entrypoint: $v is required" >&2; exit 1; }
done
[[ $NODE_ID =~ ^[0-9]+$ && $NODE_NAME =~ ^[a-z0-9]+$ && $PEER_HOST =~ ^[a-z0-9]+$ ]] ||
  { echo "entrypoint: bad NODE_ID, NODE_NAME or PEER_HOST" >&2; exit 1; }
[[ $PG_ROLE == primary || $PG_ROLE == standby ]] ||
  { echo "entrypoint: PG_ROLE must be primary or standby" >&2; exit 1; }

CONF=${REPMGR_CONF:-/tmp/repmgr.conf}
PEER="host=$PEER_HOST user=repmgr dbname=repmgr connect_timeout=3"
READY=/tmp/ready # the healthcheck needs this file: roles exist and the node is registered
rpid=""
# Markers live beside PGDATA on the volume (PGDATA is a subdirectory), so they survive a wipe
# of the data directory.
INITIALIZED=$(dirname "$PGDATA")/.initialized # this volume has held a cluster: never initdb again
REJOINING=$(dirname "$PGDATA")/.rejoining     # a rewind or clone did not finish: data is unusable
# Current timeline as 8 hex digits; empty on a standby.
TL_SQL="SELECT CASE WHEN pg_is_in_recovery() THEN '' ELSE substr(pg_walfile_name(pg_current_wal_lsn()), 1, 8) END"

log() { echo "entrypoint[$NODE_NAME]: $*"; }

stop() {
  trap - TERM INT
  log "stopping"
  rm -f "$READY"
  [ -z "$rpid" ] || kill "$rpid" 2>/dev/null || true
  pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null 2>&1 || true
  exit "${1:-0}"
}
trap stop TERM INT

pgpass_escape() { printf '%s' "$1" | sed 's/[\\:]/\\&/g'; }

write_config() {
  (
    umask 077
    {
      printf '*:*:*:postgres:%s\n' "$(pgpass_escape "$POSTGRES_PASSWORD")"
      printf '*:*:*:repmgr:%s\n' "$(pgpass_escape "$REPMGR_PASSWORD")"
      printf '*:*:*:probe:%s\n' "$(pgpass_escape "$PROBE_PASSWORD")"
    } >"$PGPASSFILE"
  )
  sed -e "s/@NODE_ID@/$NODE_ID/g" -e "s/@NODE_NAME@/$NODE_NAME/g" /etc/repmgr.conf.tmpl >"$CONF"
}

# Prints primary, standby or unreachable.
peer_role() {
  case "$(psql "$PEER" -XAtc 'SELECT pg_is_in_recovery()' 2>/dev/null)" in
    f) echo primary ;;
    t) echo standby ;;
    *) echo unreachable ;;
  esac
}

start_pg() { pg_ctl -D "$PGDATA" -w -t 120 start; }

init_primary() {
  log "empty data directory: initdb as primary"
  local pwfile
  pwfile=$(mktemp)
  printf '%s\n' "$POSTGRES_PASSWORD" >"$pwfile"
  initdb -D "$PGDATA" --username=postgres --pwfile="$pwfile" --auth=scram-sha-256 \
    --data-checksums --encoding=UTF8 --locale=C.UTF-8
  rm -f "$pwfile"
  echo "include = '/etc/postgresql/postgresql.conf'" >>"$PGDATA/postgresql.conf"
  start_pg
  # shortcut: a kill between initdb and the end of this function leaves a half-set-up node;
  # recover with `make nuke`. Add a marker file if that ever happens outside first boot.
  # Passwords come in with \getenv (not -v, which shows in `ps`), and a failed statement is not
  # logged, so no password can reach the container log (PRD 9).
  psql -X -v ON_ERROR_STOP=1 -U postgres -d postgres <<'SQL'
SET log_min_error_statement = panic;
\getenv repmgr_pw REPMGR_PASSWORD
CREATE ROLE repmgr SUPERUSER REPLICATION LOGIN PASSWORD :'repmgr_pw';
CREATE DATABASE repmgr OWNER repmgr;
CREATE DATABASE blastradius;
REVOKE CONNECT ON DATABASE repmgr, postgres, blastradius FROM PUBLIC;
SQL
  # Least-privilege roles (PRD 6.4). Tables are created and owned by migrator; app gets DML only.
  psql -X -v ON_ERROR_STOP=1 -U postgres -d blastradius <<'SQL'
SET log_min_error_statement = panic;
\getenv app_pw APP_PASSWORD
\getenv mig_pw MIGRATOR_PASSWORD
\getenv probe_pw PROBE_PASSWORD
CREATE ROLE migrator LOGIN PASSWORD :'mig_pw';
CREATE ROLE app      LOGIN PASSWORD :'app_pw';
CREATE ROLE probe    LOGIN PASSWORD :'probe_pw';
GRANT pg_monitor TO probe;
GRANT CONNECT ON DATABASE blastradius TO app, migrator, probe;
GRANT CREATE, USAGE ON SCHEMA public TO migrator;
GRANT USAGE ON SCHEMA public TO app;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;
SQL
  repmgr -f "$CONF" primary register
}

clone_standby() {
  # Never wipe unless there is a primary to copy from; exiting leaves the decision to the next boot.
  [ "$(peer_role)" = primary ] || { log "peer is not primary: not wiping for a clone"; exit 1; }
  log "cloning from $PEER_HOST"
  touch "$REJOINING"
  mkdir -p "$PGDATA"
  find "$PGDATA" -mindepth 1 -delete
  repmgr -f "$CONF" -d "$PEER" standby clone --fast-checkpoint
  start_pg
  repmgr -f "$CONF" standby register --force
  rm -f "$REJOINING"
}

# Timeline of the stopped local cluster, 8 hex digits: the newest history file, which promotion
# writes before the first write is accepted (timeline 1 has none).
local_tl() {
  local f
  f=$(find "$PGDATA/pg_wal" -maxdepth 1 -name '*.history' -printf '%f\n' | sort | tail -n 1)
  printf '%.8s\n' "${f:-00000001}"
}

# must_yield MINE THEIRS: with two primaries the older timeline loses, and on equal timelines
# the higher node name does. Both nodes evaluate the same rule, so exactly one yields.
must_yield() { [[ $1 < $2 || ($1 == "$2" && $NODE_NAME > $PEER_HOST) ]]; }

# Fencing (PRD 6.4 step 3): the peer is primary and this node was one too, so rewind and follow.
rejoin() {
  log "peer $PEER_HOST is primary: rejoining as standby"
  # pg_is_in_recovery() flips about a second before `repmgr standby promote` updates
  # repmgr.nodes; node rejoin fails until the peer is also registered as primary.
  local tries=0
  until [ "$(psql "$PEER" -XAtc "SELECT count(*) FROM repmgr.nodes WHERE node_name = '$PEER_HOST' AND type = 'primary' AND active" 2>/dev/null)" = 1 ]; do
    tries=$((tries + 1))
    [ "$tries" -lt 30 ] || break
    sleep 1
  done
  touch "$REJOINING" # a rewind killed half-way must never be started as a primary
  if ! repmgr -f "$CONF" -d "$PEER" node rejoin --force-rewind --config-files=postgresql.conf --verbose; then
    log "rejoin failed: falling back to a full re-clone"
    pg_ctl -D "$PGDATA" -m immediate -w stop >/dev/null 2>&1 || true
    clone_standby
  fi
  rm -f "$REJOINING"
}

write_config
# After SIGKILL the old postmaster.pid can name a PID that is alive in this new container,
# which makes postgres (and pg_rewind) refuse to start. Nothing else runs here yet.
rm -f "$PGDATA/postmaster.pid"
if [ ! -s "$PGDATA/global/pg_control" ] || [ -f "$REJOINING" ]; then
  # No usable data. initdb only on a volume that never held a cluster and with no live primary
  # next to it; otherwise the only safe source is the peer, however long it takes to appear.
  if [ "$PG_ROLE" = primary ] && [ ! -f "$INITIALIZED" ] && [ "$(peer_role)" != primary ]; then
    init_primary
  else
    until [ "$(peer_role)" = primary ]; do
      log "no usable data: waiting for primary $PEER_HOST to clone from"
      sleep 2
    done
    clone_standby
  fi
elif [ -f "$PGDATA/standby.signal" ]; then
  start_pg
elif [ "$(peer_role)" = primary ]; then
  # Both were primary. Rejoining unconditionally would let a stale primary that booted first
  # make the node with the newer data rewind itself, so apply the same rule as the running check.
  if must_yield "$(local_tl)" "$(psql "$PEER" -XAtc "$TL_SQL" 2>/dev/null || true)"; then
    rejoin
  else
    log "peer $PEER_HOST is primary on an older timeline: starting as primary, the peer will yield"
    start_pg
  fi
else
  log "peer is not a primary: starting in last role (primary)"
  start_pg
fi

if [ "$(psql -XAtU repmgr -d repmgr -c 'SELECT pg_is_in_recovery()')" = t ]; then
  # A former primary keeps its old standby's slot, which would pin WAL forever on a standby.
  psql -XAtU repmgr -d repmgr -c \
    "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE NOT active AND slot_type = 'physical'"
fi

# True when both nodes are primary and this one must yield (must_yield). Covers the races the
# start-up check cannot see (peer promoted, or both booted, while this node was starting).
lost_split_brain() {
  [ ! -f "$PGDATA/standby.signal" ] || return 1
  local mine theirs
  theirs=$(psql "$PEER" -XAtc "$TL_SQL" 2>/dev/null) || return 1 # peer down
  mine=$(psql -XAtU repmgr -d repmgr -c "$TL_SQL" 2>/dev/null) || return 1
  [[ -n $mine && -n $theirs ]] || return 1
  must_yield "$mine" "$theirs"
}

touch "$INITIALIZED" "$READY"
log "ready"
while pg_ctl -D "$PGDATA" status >/dev/null 2>&1; do
  # repmgrd exits when its upstream is down at start-up, so keep restarting it.
  if [ -z "$rpid" ] || ! kill -0 "$rpid" 2>/dev/null; then
    repmgrd -f "$CONF" --daemonize=false --no-pid-file &
    rpid=$!
  fi
  if lost_split_brain; then
    log "two primaries: this node yields and will rejoin on restart"
    stop 1
  fi
  sleep 2 &
  wait $!
done
log "postgres exited"
stop 1
