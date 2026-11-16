# Runbook: Postgres replication

Two nodes, `pg1` and `pg2`, run the same image (PostgreSQL 17.12, repmgr 5.5.0). One is primary,
the other an asynchronous streaming standby. `repmgrd` in each container promotes the standby
when the primary has been unreachable for 3 attempts 5 s apart. All commands run from the
repository root with the stack up (`make up`).

## Look first

```sh
make pgstatus    # role and lag of each node through a SQL probe; fails unless exactly one primary
docker exec blastradius-pg1-1 repmgr -f /tmp/repmgr.conf cluster show
docker exec blastradius-pg1-1 repmgr -f /tmp/repmgr.conf cluster event --limit 10
docker logs --tail 50 blastradius-pg1-1    # lines starting "entrypoint[pg1]:" explain start-up decisions
```

Passwords are read from `/tmp/pgpass` inside the containers (`PGPASSFILE` is set in the image),
so `docker exec ... psql -U repmgr -d repmgr` works without typing one.

## Automatic failover (nothing to do)

If the primary's container dies, the standby promotes itself about 11 to 14 s later (measured,
`docs/evidence/M2.md`). Docker does not restart a container that was killed through the Docker
API, so start the old primary yourself:

```sh
docker compose -f deploy/compose.yaml --env-file .env start pg1
```

Its entrypoint sees that the peer is primary and rejoins as a standby (next section).
`make failover-drill` runs this whole sequence and prints the timings.

## Manual failover (switchover)

Use this to move the primary on purpose, for example before maintenance. Run it on the standby
(here `pg2`):

```sh
docker exec blastradius-pg2-1 repmgr -f /tmp/repmgr.conf standby promote   # only if the primary is already down
```

If the old primary is still running, stop it first so it cannot take writes
(`docker compose ... stop pg1`), promote `pg2`, then start `pg1`; it rejoins as a standby.
`repmgr standby switchover` is not used: it needs SSH between the nodes, which the containers
do not have.

## Rejoin of an old primary

Automatic at container start. The entrypoint (`deploy/postgres/entrypoint.sh`) applies the
fencing rule:

| Data directory | Peer | Action |
| --- | --- | --- |
| empty, `PG_ROLE=primary`, volume never used | not primary | `initdb`, create roles, `repmgr primary register` |
| empty or marked `.rejoining`, any other case | any | wait for a primary, `repmgr standby clone`, register |
| has `standby.signal` | any | start as a standby |
| was primary | primary on a newer timeline | `repmgr node rejoin --force-rewind`; if that fails, wipe and re-clone |
| was primary | primary on an older timeline | start as primary; the peer yields within 2 s and rejoins |
| was primary | standby or unreachable | start as primary (last role) |

The data directory is `/var/lib/postgresql/data/pgdata`. Two marker files sit beside it on the
volume: `.initialized` (the volume has held a cluster, so an empty data directory is cloned from
the peer and never `initdb`-ed again) and `.rejoining` (a rewind or clone did not finish; the
data is unusable and the node waits for a primary to clone from).

While running, each primary checks its peer every 2 s. If both are primary, the one on the older
timeline (on a tie, the higher node name) shuts down, restarts and rejoins. To rejoin by hand:

```sh
docker compose -f deploy/compose.yaml --env-file .env restart pg1
```

`pg_rewind` discards whatever the old primary committed after the standby's last received WAL.
That is the RPO of asynchronous replication (ADR 0001).

## Full re-clone

When a node will not rejoin (rewind fails, corrupted or lost volume):

```sh
make reclone NODE=pg1     # stop, remove the data directory, start; the entrypoint clones from the peer
make pgstatus
```

Only re-clone a node while the other one is primary. Emptying both data directories destroys the
database; that case is the restore procedure (M5).

## Known limits

- No witness: a network partition between `pg1` and `pg2` can promote the standby while the
  primary is still alive. The two-primary check resolves it when they see each other again, and
  the losing node's writes are discarded (ADR 0002).
- If both nodes are stopped while the old primary has not rejoined yet, and only the old primary is
  started, it serves stale data as primary until the peer comes back. The peer then starts as
  primary too (newer timeline), the stale node yields within about 2 s and rejoins, and writes it
  took meanwhile are discarded. `make stale-primary-drill` exercises this.
- A node marked `.rejoining` waits for a primary for ever. If the peer is gone for good and the
  local data is known to be intact (the rejoin failed before `pg_rewind` ran), remove the marker
  by hand to start it in its last role.
- A standby that falls more than 1 GB of WAL behind (`max_slot_wal_keep_size`) loses its slot and
  needs `make reclone`.
- A standby started while no primary is reachable stays a read-only standby; `repmgrd` only starts
  monitoring once it has seen its upstream. Promote it by hand if the primary is gone for good.
- WAL archiving is a no-op (`archive_command = '/bin/true'`) until pgBackRest is configured in M5.
