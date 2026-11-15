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
  psql -X -v ON_ERROR_STOP=1 -U postgres -d postgres -v repmgr_pw="$REPMGR_PASSWORD" <<'SQL'
CREATE ROLE repmgr SUPERUSER REPLICATION LOGIN PASSWORD :'repmgr_pw';
CREATE DATABASE repmgr OWNER repmgr;
CREATE DATABASE blastradius;
SQL
  # Least-privilege roles (PRD 6.4). Tables are created and owned by migrator; app gets DML only.
  psql -X -v ON_ERROR_STOP=1 -U postgres -d blastradius \
    -v app_pw="$APP_PASSWORD" -v mig_pw="$MIGRATOR_PASSWORD" -v probe_pw="$PROBE_PASSWORD" <<'SQL'
CREATE ROLE migrator LOGIN PASSWORD :'mig_pw';
CREATE ROLE app      LOGIN PASSWORD :'app_pw';
CREATE ROLE probe    LOGIN PASSWORD :'probe_pw';
GRANT pg_monitor TO probe;
GRANT CREATE, USAGE ON SCHEMA public TO migrator;
GRANT USAGE ON SCHEMA public TO app;
ALTER DEFAULT PRIVILEGES FOR ROLE migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app;
SQL
  repmgr -f "$CONF" primary register
}

clone_standby() {
  log "cloning from $PEER_HOST"
  find "$PGDATA" -mindepth 1 -delete
  repmgr -f "$CONF" -d "$PEER" standby clone --fast-checkpoint
  start_pg
  repmgr -f "$CONF" standby register --force
}

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
  if ! repmgr -f "$CONF" -d "$PEER" node rejoin --force-rewind --config-files=postgresql.conf --verbose; then
    log "rejoin failed: falling back to a full re-clone"
    pg_ctl -D "$PGDATA" -m immediate -w stop >/dev/null 2>&1 || true
    clone_standby
  fi
}

write_config
# After SIGKILL the old postmaster.pid can name a PID that is alive in this new container,
# which makes postgres (and pg_rewind) refuse to start. Nothing else runs here yet.
rm -f "$PGDATA/postmaster.pid"
if [ ! -s "$PGDATA/global/pg_control" ]; then
  # pg_basebackup writes pg_control last, so a missing file also covers a half-finished clone.
  if [ "$PG_ROLE" = standby ]; then
    until [ "$(peer_role)" = primary ]; do
      log "waiting for primary $PEER_HOST"
      sleep 2
    done
    clone_standby
  elif [ "$(peer_role)" = primary ]; then
    clone_standby # never initdb a second primary next to a live one
  else
    init_primary
  fi
elif [ -f "$PGDATA/standby.signal" ]; then
  start_pg
elif [ "$(peer_role)" = primary ]; then
  rejoin
else
  log "peer is not a primary: starting in last role (primary)"
  start_pg
fi

if [ "$(psql -XAtU repmgr -d repmgr -c 'SELECT pg_is_in_recovery()')" = t ]; then
  # A former primary keeps its old standby's slot, which would pin WAL forever on a standby.
  psql -XAtU repmgr -d repmgr -c \
    "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE NOT active AND slot_type = 'physical'"
fi

# True when both nodes are primary and this one must yield: the older timeline loses, and on
# equal timelines the higher node name does. Both nodes evaluate the same rule, so exactly one
# yields. Covers the races the start-up check cannot see (peer promoted, or both booted,
# while this node was starting).
lost_split_brain() {
  [ ! -f "$PGDATA/standby.signal" ] || return 1
  local tl="SELECT substr(pg_walfile_name(pg_current_wal_lsn()), 1, 8)" mine theirs
  theirs=$(psql "$PEER" -XAtc "$tl" 2>/dev/null) || return 1 # fails while the peer is down or a standby
  mine=$(psql -XAtU repmgr -d repmgr -c "$tl" 2>/dev/null) || return 1
  [[ -n $mine && -n $theirs ]] || return 1
  [[ $mine < $theirs || ($mine == "$theirs" && $NODE_NAME > $PEER_HOST) ]]
}

touch "$READY"
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
