# ADR 0001: Asynchronous streaming replication

Status: accepted (M2). Author: Adarsh Tripathi.

## Decision

`pg2` follows `pg1` with asynchronous physical streaming replication through a replication slot.
`synchronous_standby_names` stays empty.

## Why

With one replica, synchronous replication blocks every write whenever that replica is down, and a
third node does not fit the 1.1 GB always-on budget (PRD 2, 12.1).

## Consequences

- Low write latency; writes continue when the standby is lost.
- A write acknowledged just before the primary dies can be missing after failover (RPO above 0).
  `pg_rewind` discards it when the old primary rejoins. E4 measures this as `rpo_seconds` and
  `lost_acked_writes`.
- The slot retains WAL on the primary while the standby is down. A long outage needs the standby
  back or the slot dropped before the volume fills.
