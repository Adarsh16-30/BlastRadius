# ADR 0002: Two database nodes, no witness

Status: accepted (M2). Author: Adarsh Tripathi.

## Decision

The cluster is two repmgr nodes with `repmgrd` automatic failover and no witness node.

## Why

A witness costs about 64 MB that the memory budget does not have (PRD 2, 12.1). The faults in
scope are node deaths, not partitions between `pg1` and `pg2`.

## Consequences

- A standby that cannot reach the primary promotes itself; it cannot tell a dead primary from a
  partition. Under a partition both nodes can accept writes until they see each other again.
- Mitigation, in `deploy/postgres/entrypoint.sh`: a node that starts while its peer is primary
  rejoins as a standby, and a running primary that sees a second primary on a newer timeline
  shuts down and rejoins. The losing node's writes are discarded.
- A repmgr witness capped at 64 MB is the stretch goal that removes this limit.
