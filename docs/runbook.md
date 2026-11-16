# Runbook: Postgres replication

Two nodes, `pg1` and `pg2`, run the same image (PostgreSQL 17.9, repmgr 5.5.0). One is primary,
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
| empty, `PG_ROLE=standby` | any | wait for a primary, `repmgr standby clone`, register |
| empty, `PG_ROLE=primary` | primary | clone as a standby (never a second `initdb`) |
| empty, `PG_ROLE=primary` | not primary | `initdb`, create roles, `repmgr primary register` |
| has `standby.signal` | any | start as a standby |
| was primary | primary | `repmgr node rejoin --force-rewind`; if that fails, wipe and re-clone |
| was primary | standby or unreachable | start as primary (last role) |

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
make reclone NODE=pg1     # stop, empty the data directory, start; the entrypoint clones from the peer
make pgstatus
```

Only re-clone a node while the other one is primary. Emptying both data directories destroys the
database; that case is the restore procedure (M5).

## Known limits

- No witness: a network partition between `pg1` and `pg2` can promote the standby while the
  primary is still alive. The two-primary check resolves it when they see each other again, and
  the losing node's writes are discarded (ADR 0002).
- If both nodes are stopped while the old primary has not rejoined yet, and only the old primary is
  started, it serves stale data as primary until the peer comes back, then the older timeline yields.
- A standby started while no primary is reachable stays a read-only standby; `repmgrd` only starts
  monitoring once it has seen its upstream. Promote it by hand if the primary is gone for good.
- WAL archiving is a no-op (`archive_command = '/bin/true'`) until pgBackRest is configured in M5.
