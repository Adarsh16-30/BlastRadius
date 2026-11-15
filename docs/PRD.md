# BlastRadius PRD

Nov 15, 2026 · @Adarsh Tripathi

## 1. Overview

BlastRadius is a chaos-engineering harness that injects five scripted faults into a small production-style service and fails the run unless measured recovery targets are met. It is a portfolio project for SRE, DevOps and platform roles, built by one student with a coding assistant.

The system under test is a Go HTTP API behind HAProxy, backed by a PostgreSQL primary and one streaming replica. The harness is a Go CLI named `blast`. Every run produces a JSON scorecard with recovery time (RTO), data loss (RPO), error rate and latency.

### Goals

1. Survive the loss of one API instance with zero failed client requests.
2. Fail over the database automatically, with RTO of 30 s or less and RPO of 1 s or less.
3. Restore the database from backup to a verified, checksum-matching state in 5 minutes or less.
4. Degrade gracefully: reads keep working and writes return a fast 503 when no primary is writable.
5. Run the full suite nightly in GitHub Actions and fail the build on regression.
6. Keep the whole stack under 1.5 GB of RAM with a hard cap on every container.

### Non-goals

- No Kubernetes. The stack runs on Docker Compose only.
- No LLM or AI-driven remediation. Every fault and every recovery is scripted and repeatable.
- No multi-region or multi-host deployment. One host simulates the cluster.
- No zero-data-loss guarantee. Replication is asynchronous by design (see section 12).
- No general-purpose chaos framework. The five experiments in section 7 are the scope.

### Success criteria

The project is done when `make chaos` passes all five experiments three times in a row on the developer's laptop, the nightly workflow is green, and the README shows numbers copied from real scorecards.

## 2. Hard constraints

These four constraints override every other design choice. If a requirement elsewhere conflicts with one, the constraint wins and the conflict is reported.

| Constraint | Rule |
| --- | --- |
| Memory | Always-on containers total 1.1 GB or less. Peak with load and chaos tools is 1.35 GB or less. Every Compose service sets `mem_limit`, `mem_reservation` and `pids_limit`. |
| Cost | $0. No paid service, no cloud resource that bills, no licence fee. |
| Host | An 8 GB laptop running Linux, or Windows with WSL2 capped by `.wslconfig` (`memory=3GB`, `swap=1GB`). Also `ubuntu-latest` GitHub runners. |
| Runtime | Docker Engine with Compose v2. No Kubernetes, no JVM, no Node.js service in the running stack. |

On native Linux the stack also runs inside a systemd scope as a backstop: `systemd-run --scope -p MemoryMax=2G -p MemorySwapMax=0 docker compose up`.

Go services set `GOMEMLIMIT` to 75% of their container limit so the garbage collector reacts before the kernel does.

## 3. Architecture

Client traffic flows down one path: load generator, HAProxy, two API instances, Toxiproxy, then Postgres. The chaos runner and the monitoring stack sit beside that path and never inside it.

&#91;embedded content: runtime architecture · 12 services on one Compose network\]

Application connections reach Postgres only through Toxiproxy, so network faults hit the API and leave replication and backups untouched.

### Normal request

1. k6 or the sequence writer sends a request to HAProxy on port 8080.
2. HAProxy picks a healthy API instance.
3. The API runs the query through its write pool or read pool, via Toxiproxy.
4. Writes go to the primary. The standby receives them through asynchronous streaming replication.

### Database failover

1. The primary dies. Writes start failing, the circuit breaker opens, and the API enters read-only mode.
2. `repmgrd` on the standby fails to reach the primary three times at 5 s intervals, then promotes the standby.
3. The API's write pool reconnects. `target_session_attrs=read-write` selects the new primary, the breaker half-opens, and writes resume.
4. When the old primary's container starts again, its entrypoint sees the peer is primary and rejoins as a standby with `pg_rewind`.

### Restore

Both data volumes are wiped, pgBackRest restores the latest backup and replays archived WAL onto `pg1`, and `pg2` is cloned from it.

## 4. Tech stack and memory budget

The stack is twelve Compose services with a combined limit of 1,328 MB, of which 1,088 MB is always on. The limits are budgets, not measurements: record real usage with `docker stats --no-stream` in milestone M1 and adjust.

| Service | Technology | Role | `mem_limit` | Runs |
| --- | --- | --- | --- | --- |
| `haproxy` | HAProxy 3.x LTS | Edge load balancer, health checks, retries, built-in Prometheus exporter | 32 MB | Always |
| `api1`, `api2` | Go, `net/http`, `pgx/v5`, `sony/gobreaker`, OpenTelemetry SDK | System under test | 64 MB each | Always |
| `toxiproxy` | Shopify Toxiproxy 2.x | TCP proxy between API and Postgres for latency and resets | 32 MB | Always |
| `pg1`, `pg2` | PostgreSQL 17 + repmgr 5.5+ + pgBackRest | Primary and streaming replica with automatic failover | 256 MB each | Always |
| `prometheus` | Prometheus 3.x | Metrics, 15 s scrape, 2-day retention | 160 MB | Always |
| `grafana` | Grafana OSS | Dashboards, alerts, chaos annotations | 192 MB | Always |
| `dockerproxy` | `tecnativa/docker-socket-proxy` | Restricted Docker API for the chaos runner | 32 MB | Always |
| `cloudflared` | Cloudflare Tunnel | Public URL for Grafana | 48 MB | Profile `public` |
| `blast` | Go CLI (this project) | Chaos runner and sequence writer | 64 MB | Profile `chaos` |
| `k6` | Grafana k6 | HTTP load and latency measurement | 128 MB | Profile `chaos` |

### Version policy

Pin every image to an exact version tag in `compose.yaml`, chosen from the newest stable release at build time. The versions above are minimums. Check each tool's official documentation before pinning; do not rely on remembered version numbers or flags.

### PostgreSQL memory settings

`shared_buffers=32MB`, `work_mem=2MB`, `maintenance_work_mem=16MB`, `max_connections=30`, `max_wal_senders=5`, `wal_keep_size=256MB`.

### Tools that run and exit

These add no standing memory: golangci-lint, Hadolint, Trivy, OpenTofu or Terraform, and Ansible (the last two only for the optional cloud VM in section 10).

## 5. Repository layout

One public repository holds everything, so GitHub Actions and the container registry stay free.

```text
blastradius/
  cmd/
    api/                 # HTTP service entrypoint
    blast/               # chaos runner CLI entrypoint
  internal/
    api/                 # handlers, middleware, routing
    store/               # pgx pools, queries, retries, breaker
    chaos/               # experiment engine, fault drivers, probes
    scorecard/           # scorecard model, baseline comparison, HTML report
    seqwriter/           # monotonic sequence writer for RPO
  db/
    migrations/          # numbered .sql files
    seed/                # seed generator for the restore drill
  deploy/
    compose.yaml         # all services, limits, profiles
    haproxy/haproxy.cfg
    postgres/            # Dockerfile, entrypoint.sh, postgresql.conf, pg_hba.conf, repmgr.conf.tmpl, pgbackrest.conf
    prometheus/prometheus.yml
    grafana/             # provisioning: datasources, dashboards (JSON), alert rules
    toxiproxy/proxies.json
  experiments/           # one YAML file per experiment
  load/                  # k6 scripts
  scorecards/            # baseline.json plus committed nightly results
  infra/                 # optional: OpenTofu and Ansible for a cloud VM
  .github/workflows/     # ci.yml, chaos-nightly.yml, restore-weekly.yml
  docs/                  # architecture.md, runbook.md, decisions/ (ADRs)
  Makefile
  .env.example
  README.md
```

### Makefile targets

| Target | Does |
| --- | --- |
| `make up` / `make down` | Start or stop the always-on stack. `down` keeps volumes; `make nuke` removes them. |
| `make test` | `go vet`, `golangci-lint`, `go test -race ./...` |
| `make chaos` | Run every experiment in `experiments/` and write a scorecard |
| `make chaos EXP=primary-kill` | Run one experiment |
| `make backup` / `make restore-drill` | Take a pgBackRest backup; run the full restore experiment |
| `make stats` | Print `docker stats --no-stream` against each limit |
| `make public` | Start the `public` profile and print the tunnel URL |

## 6. Component specifications

Each subsection states the behaviour the coding assistant must implement; anything not stated is the assistant's choice, provided section 2 holds.

### 6.1 API service (`cmd/api`)

**Endpoints**

| Method and path | Behaviour |
| --- | --- |
| `GET /healthz` | 200 if the process is alive. No database call. |
| `GET /readyz` | 200 if any database host answers `SELECT 1` within 500 ms, else 503. |
| `POST /v1/orders` | Insert an order. Requires an `Idempotency-Key` header. A repeated key returns the original row with 200. |
| `GET /v1/orders/{id}` | Read one order. |
| `GET /v1/orders?limit=N` | List the newest N orders, N capped at 100. |
| `POST /v1/seq` | Body `{writer_id, seq}`. Insert into `seq_log`. Used only by the sequence writer. |
| `GET /v1/seq/{writer_id}` | Return every `seq` stored for that writer. |
| `GET /metrics` | Prometheus metrics from the OpenTelemetry SDK's Prometheus exporter. |
| `POST /debug/alloc?mb=N` | Allocate and hold N MB. A sibling route, POST /debug/crash, exits the process with status 1. Both are registered only when `CHAOS_ENABLED=true`. |

**Database access**

- Two `pgxpool` pools per instance, both pointed at the Toxiproxy listeners, never at Postgres directly.
- Write pool: multi-host DSN with `target_session_attrs=read-write`, max 6 connections.
- Read pool: multi-host DSN with `target_session_attrs=any`, max 4 connections.
- Connect timeout 2 s. `statement_timeout` 1 s set per connection. Pool health check every 5 s.

**Resilience rules**

1. Every request has a 2 s total deadline, propagated through `context`.
2. Reads retry up to 2 times on connection errors and serialization failures. Backoff is exponential with full jitter, base 50 ms, cap 400 ms.
3. Writes retry up to 2 times on connection errors only. This is safe because `idempotency_key` and `(writer_id, seq)` are unique.
4. One circuit breaker guards the write pool. It opens when 50% or more of the last 20 calls failed, and half-opens after 5 s.
5. When the breaker is open or no host is read-write, writes return 503 with `Retry-After: 5` within 100 ms. Reads keep being served from the read pool. This is read-only mode.
6. On SIGTERM the server stops accepting connections, drains for up to 10 s, then closes the pools.

**Metrics to export**

`http_requests_total{route,method,code}`, `http_request_duration_seconds` histogram, `db_query_duration_seconds{pool}` histogram, `db_retries_total{pool}`, `breaker_state` gauge (0 closed, 1 half-open, 2 open), `readonly_mode` gauge, `db_pool_acquired_conns{pool}`.

**Other requirements**

- Structured JSON logs with `log/slog`, including a request ID on every line.
- Configuration through environment variables only, validated at start-up; an invalid value exits non-zero with a clear message.
- Final image is distroless or scratch, runs as a non-root user, and is under 30 MB.

### 6.2 Database schema (`db/migrations`)

```sql
CREATE TABLE orders (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  customer_id     text NOT NULL,
  amount_cents    bigint NOT NULL CHECK (amount_cents > 0),
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_created_at_idx ON orders (created_at DESC);

CREATE TABLE seq_log (
  writer_id  text NOT NULL,
  seq        bigint NOT NULL,
  written_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (writer_id, seq)
);
```

Migrations run from a one-shot `migrate` Compose service (`api migrate`) under a Postgres advisory lock, before the API instances start.

### 6.3 HAProxy (`deploy/haproxy`)

- One frontend on port 8080, one backend with `api1` and `api2`, `balance roundrobin`.
- Active health check: `GET /healthz` every 2 s, `fall 2`, `rise 2`.
- `retries 2`, `option redispatch`, and `retry-on conn-failure empty-response`. Replaying a POST is safe because of idempotency keys.
- Timeouts: connect 1 s, server 3 s, client 5 s.
- Requests to `/debug/` and `/metrics` are denied at the frontend with 403.
- The built-in Prometheus exporter is served on a separate internal port.

### 6.4 PostgreSQL with repmgr (`deploy/postgres`)

One custom image, built from the official `postgres:17` image, adds repmgr and pgBackRest. Both nodes run the same image and differ only by environment variables.

**Settings**

- `wal_level=replica`, `hot_standby=on`, `wal_log_hints=on`, `archive_mode=on`, data checksums enabled at `initdb`.
- Asynchronous streaming replication with a physical replication slot. `synchronous_standby_names` stays empty.
- repmgr: `failover=automatic`, `reconnect_attempts=3`, `reconnect_interval=5`, `monitor_interval_secs=2`, `use_replication_slots=yes`. `repmgrd` runs in both containers.

**Entrypoint logic (`entrypoint.sh`)**

1. Empty data directory and `PG_ROLE=primary`: run `initdb`, start Postgres, create roles, register as primary.
2. Empty data directory and `PG_ROLE=standby`: wait for the peer, run `repmgr standby clone`, start, register as standby.
3. Existing data directory: before starting, ask the peer whether it is a primary. If it is, rejoin as a standby with `repmgr node rejoin --force-rewind`. If the peer is unreachable, start in the node's last role.
4. Start `repmgrd` after Postgres accepts connections.

Step 3 is the fencing rule. It stops a restarted old primary from accepting writes after a failover.

**Roles**

| Role | Privileges | Used by |
| --- | --- | --- |
| `app` | DML on `orders` and `seq_log` only | API |
| `migrator` | DDL on the application schema | `migrate` service |
| `repmgr` | Superuser and replication, as repmgr requires | repmgr, repmgrd |
| `probe` | `pg_monitor` | chaos runner probes |

### 6.5 Backups with pgBackRest

- One stanza named `main`. The repository is a named volume mounted into both Postgres containers.
- `archive_command` pushes WAL with `pgbackrest archive-push`. Only the current primary archives.
- `make backup` finds the current primary and runs a full backup there. Retention keeps two full backups.
- Restore procedure, also used by experiment E5: stop both nodes, wipe both data volumes, restore the latest backup onto `pg1`, start it as primary, verify, then re-clone `pg2` with `repmgr standby clone`.
- The shared-volume repository works only on a single host. Record this in an ADR.

### 6.6 Toxiproxy

Two proxies defined in `proxies.json`: `pg1` (listen 15432, upstream `pg1:5432`) and `pg2` (listen 15433, upstream `pg2:5432`). The API's DSNs name these listeners. repmgr and pgBackRest talk to Postgres directly, so injected faults affect only application traffic.

### 6.7 Load generation

- **k6** (`load/steady.js`): constant arrival rate, 70% reads and 30% writes, with rate and duration from environment variables. It writes a JSON summary that `blast` parses for latency percentiles and failure counts.
- **Sequence writer** (`blast seqwriter`): one goroutine posts `seq = 1, 2, 3…` to `/v1/seq` every 50 ms and records the send time, acknowledgement time and status of each write in memory. It never retries a sequence number itself.

### 6.8 Observability

- **Prometheus** scrapes `api1`, `api2`, HAProxy, Toxiproxy and itself every 15 s. Retention is 2 days. Recording rules precompute request rate, error ratio and p50/p95/p99 latency.
- **Grafana** is fully provisioned from files: one datasource, three dashboards (Service health, Database and failover, Chaos runs), and alert rules. No manual clicking.
- **Alerts** (Grafana-managed): fast burn (error budget burning 14.4 times too fast over 5 minutes and 1 hour), slow burn (6 times over 30 minutes and 6 hours), and `readonly_mode == 1` for more than 30 s.
- **Annotations**: `blast` posts a Grafana annotation at fault start and fault end, tagged with the experiment name.
- Anonymous access is enabled with the Viewer role only. The admin password comes from `.env`.

## 7. Chaos runner (`cmd/blast`)

`blast` reads declarative experiment files, drives load, injects one fault, measures recovery, and exits non-zero if any assertion fails.

### 7.1 Commands

| Command | Does |
| --- | --- |
| `blast run <file-or-dir> --out scorecards/<run-id>.json` | Run one or all experiments in sequence |
| `blast compare --baseline scorecards/baseline.json <scorecard>` | Exit non-zero on regression (section 8) |
| `blast report --last 10 --out site/index.html` | Render a static HTML page of recent scorecards |
| `blast seqwriter` | Run the sequence writer standalone, for debugging |

### 7.2 Run phases

Every experiment follows the same six phases, logged with timestamps:

1. **Preflight.** All containers healthy, exactly one primary, replica lag under 1 s, no toxics active. Abort if not.
2. **Steady state.** Start load. Wait `warmup`, then confirm the steady-state probes pass.
3. **Inject.** Apply the fault and post the start annotation.
4. **Observe.** Hold for `duration` while recording measurements.
5. **Recover.** Remove the fault, restart anything stopped, and wait until preflight conditions hold again or `recovery_timeout` passes.
6. **Assert.** Evaluate every assertion, write the result, and post the end annotation.

A `defer`-style cleanup always removes toxics and restarts stopped containers, even when a run panics or is interrupted.

### 7.3 Fault drivers

| Fault type | Mechanism | Parameters |
| --- | --- | --- |
| `container_kill` | Docker API `kill` with SIGKILL through `dockerproxy` | `target`, `restart_after` |
| `container_pause` | Docker API `pause` and `unpause` | `target`, `duration` |
| `net_latency` | Toxiproxy `latency` toxic | `proxies`, `latency_ms`, `jitter_ms` |
| `net_reset` | Toxiproxy `reset_peer` toxic | `proxies`, `toxicity` (0 to 1) |
| `memory_hog` | `POST /debug/alloc` sent straight to the target container | `target`, `mb` |
| `data_loss` | Stop Postgres containers and remove their data volumes | `targets` |

`target: primary` and `target: standby` resolve at run time by querying `pg_is_in_recovery()` on each node.

A seventh fault type, `process_crash`, calls `POST /debug/crash` on the target so the process exits by itself. E1 uses it because Docker's restart policy is expected to ignore containers stopped through the Docker API; confirm this in M4 and record the result.

### 7.4 Experiment file schema

```yaml
name: primary-kill
description: Kill the Postgres primary under write load and measure failover.
load:
  k6: { script: load/steady.js, rps: 20, duration: 150s }
  seqwriter: { enabled: true, interval: 50ms }
warmup: 20s
fault:
  type: container_kill
  target: primary
  restart_after: 60s        # start the old primary again to test rejoin
duration: 90s
recovery_timeout: 180s
assertions:
  - { metric: rto_seconds,         op: "<=", value: 30 }
  - { metric: rpo_seconds,         op: "<=", value: 1 }
  - { metric: read_error_ratio,    op: "<=", value: 0.05 }
  - { metric: rejoin_seconds,      op: "<=", value: 120 }
  - { metric: primary_count_final, op: "==", value: 1 }
```

Unknown keys, unknown metrics and missing required fields are validation errors before anything runs.

### 7.5 Experiment catalogue

| ID | File | Fault | Load | Pass conditions |
| --- | --- | --- | --- | --- |
| E1 | `api-kill.yaml` | Crash `api1` at 20 s, restart by Docker policy | 30 RPS, 60 s | 0 failed client requests; two healthy backends again within 10 s |
| E2 | `db-latency.yaml` | 300 ms latency with 50 ms jitter on both proxies, plus `reset_peer` at toxicity 0.05, for 60 s | 30 RPS, 100 s | Read p99 under 1 s; read error ratio under 1%; write error ratio under 5% |
| E3 | `api-oom.yaml` | `memory_hog` of 128 MB on `api1` | 30 RPS, 60 s | `api1` reports `OOMKilled=true`; no other container restarts; `api1` healthy within 10 s; client error ratio under 1% |
| E4 | `primary-kill.yaml` | SIGKILL the primary; start it again after 60 s | 20 RPS plus sequence writer, 150 s | RTO 30 s or less; RPO 1 s or less; old primary rejoins as standby within 120 s; exactly one primary at the end |
| E5 | `restore.yaml` | `data_loss` on both nodes, then pgBackRest restore | None during restore | Restore finishes within 5 minutes; row counts and table checksums match the pre-fault snapshot; replica re-cloned and streaming |

E5 seeds 500,000 orders first (about 100 MB), takes a backup, records `count(*)` and an ordered `md5` aggregate for each table, and then destroys the data.

### 7.6 Scorecard format

```json
{
  "run_id": "2026-11-20T18-30-00Z-a1b2c3d",
  "git_sha": "a1b2c3d",
  "host": { "os": "linux", "cpus": 4, "mem_total_mb": 7820 },
  "started_at": "2026-11-20T18:30:00Z",
  "duration_seconds": 742,
  "passed": true,
  "experiments": [
    {
      "name": "primary-kill",
      "passed": true,
      "fault_at": "2026-11-20T18:36:10.412Z",
      "measurements": {
        "rto_seconds": 21.4,
        "rpo_seconds": 0.15,
        "lost_acked_writes": 3,
        "read_error_ratio": 0.012,
        "rejoin_seconds": 48.0,
        "http_p99_ms": 310
      },
      "assertions": [
        { "metric": "rto_seconds", "op": "<=", "target": 30, "actual": 21.4, "passed": true }
      ],
      "peak_memory_mb": { "pg1": 148, "pg2": 151, "api1": 31 }
    }
  ]
}
```

The numbers in this sample are illustrative. Real scorecards contain only measured values.

## 8. Targets and how each is measured

Every number below is a target to verify, and each has one fixed measurement method so results stay comparable between runs.

### 8.1 Service level objective

During any chaos run except E5, 99.5% of client requests succeed and 99% of successful requests finish within 500 ms. A request succeeds if it returns a 2xx or an intentional 503 with `Retry-After` in read-only mode; the scorecard reports both counts separately.

### 8.2 Metric definitions

| Metric | Definition | Source |
| --- | --- | --- |
| `rto_seconds` | Time from the first failed write after the fault to the first write in a run of five consecutive successes | Sequence writer log |
| `rpo_seconds` | Fault time minus the acknowledgement time of the earliest acknowledged write missing from the database afterwards; 0 if none are missing | Sequence writer log compared with `GET /v1/seq/{writer_id}` |
| `lost_acked_writes` | Count of acknowledged sequence numbers missing afterwards | Same |
| `failed_requests` | Requests with a network error or a status outside 2xx and intentional 503 | k6 summary |
| `read_error_ratio`, `write_error_ratio` | Failed divided by total, per request type, over the observe phase | k6 summary with tags |
| `http_p99_ms` | 99th percentile latency over the observe phase | k6 summary |
| `recover_seconds` | Fault time to the moment HAProxy reports two healthy backends | HAProxy stats polled every 500 ms |
| `rejoin_seconds` | Old primary start to it streaming as a standby | `pg_stat_replication` on the new primary |
| `restore_seconds` | `pgbackrest restore` start to the first successful `SELECT 1` on the restored primary | Runner timestamps |
| `data_match` | 1 if row counts and checksums equal the pre-fault snapshot | SQL comparison |
| `peak_memory_mb` | Highest memory usage per container during the run | Docker stats API sampled every 2 s |

### 8.3 Regression gate

`blast compare` fails the build when any of these is true against `scorecards/baseline.json`:

- Any assertion that passed in the baseline now fails.
- `rto_seconds`, `recover_seconds` or `restore_seconds` is more than 20% worse.
- `http_p99_ms` is more than 30% worse.
- Any container's `peak_memory_mb` exceeds 90% of its limit.

The baseline is updated only by a deliberate commit, never automatically.

### 8.4 Performance baseline

With no fault active, at 30 RPS on the developer's laptop: p99 under 100 ms, zero failed requests over 5 minutes, and replica lag under 1 s. Measure this in M1 and M2 before any chaos work.

## 9. Security requirements

The stack is a public demo that deliberately contains fault-injection endpoints, so the rules below are mandatory, not optional hardening.

**Secrets**

- All passwords and tokens come from `.env`, which is git-ignored. `.env.example` lists every variable with a placeholder.
- No secret appears in an image layer, a log line, a scorecard or the repository. CI runs a secret scan (gitleaks) on every push.
- CI generates random passwords per run.

**Network exposure**

- Only two ports are published, both bound to `127.0.0.1`: HAProxy 8080 and Grafana 3000.
- Postgres, Toxiproxy, Prometheus and `dockerproxy` are reachable only on the internal Compose network.
- The tunnel exposes Grafana only. The API is never public.
- `pg_hba.conf` allows `scram-sha-256` from the Compose subnet and nothing else.

**Containers**

- Every service: non-root user where the image allows, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`, and `read_only: true` with explicit `tmpfs` mounts where needed.
- No container is privileged. No container mounts `/var/run/docker.sock` except `dockerproxy`, which mounts it read-only.
- `dockerproxy` allows only container list, inspect, stats, events, kill, pause, unpause, start and stop. Images, exec, networks and build stay disabled. Volume removal is enabled for E5 only, and limited to the two Postgres data volumes by a name check in `blast`.

**Chaos endpoints**

- `/debug/*` routes exist only when `CHAOS_ENABLED=true`, and HAProxy returns 403 for them regardless.
- `blast` refuses to run unless `BLAST_ALLOW=true` and the target Compose project name matches `blastradius`. This prevents pointing it at anything else.

**Supply chain**

- Images pinned by exact version tag. Go dependencies pinned with `go.sum`; `govulncheck` runs in CI.
- Trivy scans both built images in CI and fails on critical vulnerabilities that have a fix.
- GitHub Actions workflows set `permissions: contents: read` by default and pin third-party actions by commit SHA.

## 10. CI/CD and deployment

GitHub Actions on a public repository is the only CI system, and the laptop plus a Cloudflare Tunnel is the only required host; both cost $0.

### 10.1 Workflows

| Workflow | Trigger | Steps | Fails when |
| --- | --- | --- | --- |
| `ci.yml` | Push and pull request | `go vet`, golangci-lint, `go test -race -cover`, Hadolint, `docker compose config`, build both images, Trivy, gitleaks, govulncheck, experiment YAML validation | Any step fails, or coverage of `internal/` drops below 70% |
| `chaos-nightly.yml` | Cron once a day, plus manual dispatch | `make up`, wait for health, `blast run experiments/` for E1 to E4, `blast compare`, upload scorecard, publish report to GitHub Pages | Any assertion or regression check fails |
| `restore-weekly.yml` | Cron once a week, plus manual dispatch | `make restore-drill` (E5), upload scorecard | Restore or verification fails |

Each chaos workflow has a 30-minute timeout, uploads all container logs as an artifact on failure, and always runs `make nuke` at the end.

### 10.2 What recruiters can see

1. A status badge in the README for the nightly chaos run.
2. A GitHub Pages site, generated by `blast report`, showing the last 10 scorecards with pass or fail per experiment. It needs no server.
3. A Grafana dashboard through `make public`, which starts `cloudflared` with a Quick Tunnel and prints a temporary URL. Use it for live demos and screen recordings.
4. A 90-second recording of E4 linked from the README, as a fallback when the laptop is off.

### 10.3 Local setup

- **Windows:** WSL2 with systemd enabled and `.wslconfig` set as in section 2, then Docker Engine inside WSL2 or Docker Desktop.
- **Linux:** Docker Engine and Compose v2.
- `cp .env.example .env`, then `make up`. First start must reach all-healthy in under 3 minutes.

### 10.4 Optional always-on host

An Oracle Cloud Always Free ARM VM can host the stack permanently. If used, `infra/` holds OpenTofu for the VM and network and an Ansible playbook that installs Docker and starts the stack. Images must then be built for `linux/arm64` as well as `linux/amd64`. This is a stretch goal and must not block any milestone.

## 11. Milestones and acceptance criteria

Build the eight milestones in order; each ends with a working, committed state, and none starts until the previous one's criteria pass.

### M0. Skeleton

- [ ] Repository layout from section 5, Go module, Makefile, `.env.example`, licence, empty README sections.
- [ ] `compose.yaml` declares every service with its limits and profiles, using placeholder images where code does not exist yet.
- [ ] `ci.yml` runs lint and tests and is green.

### M1. API on a single database

- [ ] All endpoints in 6.1 work against one Postgres node through HAProxy and Toxiproxy.
- [ ] Migrations run through the `migrate` service.
- [ ] Unit tests cover handlers and the store layer; integration tests run against a real Postgres container.
- [ ] k6 baseline from 8.4 passes at 30 RPS. `make stats` output is saved to `docs/memory-m1.txt`.

### M2. Replication and failover

- [ ] `pg2` clones from `pg1` and streams. Replica lag is visible through a SQL probe.
- [ ] Killing the primary by hand promotes the standby with no manual step.
- [ ] Restarting the old primary makes it rejoin as a standby. There is never more than one primary.
- [ ] `docs/runbook.md` describes manual failover, rejoin and full re-clone.

### M3. Resilience in the API

- [ ] Timeouts, retries with jitter, circuit breaker and read-only mode from 6.1 are implemented.
- [ ] Unit tests prove each rule with a fake store: retry counts, backoff bounds, breaker transitions, 503 with `Retry-After`.
- [ ] During a manual failover, reads keep succeeding and writes return 503 quickly, then recover without restarting the API.

### M4. Chaos runner and experiments E1 to E4

- [ ] `blast run` implements the phases in 7.2 and the fault drivers in 7.3.
- [ ] Experiment files validate against the schema in 7.4.
- [ ] E1 to E4 run end to end and write a scorecard in the 7.6 format.
- [ ] Cleanup restores a healthy stack after an interrupted run (test by pressing Ctrl+C during the observe phase).
- [ ] The first passing scorecard is committed as `scorecards/baseline.json`.

### M5. Backup and restore

- [ ] pgBackRest stanza, WAL archiving and `make backup` work.
- [ ] E5 passes: restore within 5 minutes with matching counts and checksums, and the replica re-cloned.
- [ ] A backup taken after a failover restores correctly.

### M6. Observability

- [ ] Prometheus scrapes all targets; recording rules exist.
- [ ] Three provisioned Grafana dashboards load with data and show chaos annotations.
- [ ] The three alerts fire during E4 and resolve afterwards. Screenshots go in `docs/`.

### M7. Automation and presentation

- [ ] `chaos-nightly.yml` and `restore-weekly.yml` are green three runs in a row.
- [ ] `blast compare` fails a deliberately slowed build (prove it once on a branch).
- [ ] GitHub Pages report and README badge are live.
- [ ] README contains: architecture diagram, how to run, the results table from a real scorecard, the trade-offs from section 12, and measured memory per container.

### Stretch, after M7

- A repmgr witness node capped at 64 MB, to make two-node failover safe under a network partition.
- Zero-code metrics with OpenTelemetry eBPF Instrumentation on native Linux.
- A benchmark comparing asynchronous and synchronous replication: RPO against write latency.
- The Oracle VM deployment from 10.4.

## 12. Trade-offs, risks and open decisions

Three design trade-offs are deliberate and must be written up as ADRs in `docs/decisions/`; the risks below them each have a named fallback.

### 12.1 Deliberate trade-offs

| Decision | Gained | Given up | Why |
| --- | --- | --- | --- |
| Asynchronous replication | Low write latency; writes survive losing the replica | A committed write can be lost in a failover (RPO above 0) | Synchronous replication with one replica blocks all writes when that replica dies, and a third node does not fit the memory budget |
| Two database nodes, no witness | About 64 MB saved | Safe failover under a network partition between the nodes | Faults are limited to node death, where repmgr plus the entrypoint fencing rule is sufficient. Partition between `pg1` and `pg2` is out of scope until the stretch goal |
| 15 s scrape, 2-day retention | Prometheus stays near 160 MB | Fine-grained dashboards and history | Precise numbers come from k6 and the sequence writer, not from Prometheus |

### 12.2 Risks and fallbacks

| Risk | Likely sign | Fallback |
| --- | --- | --- |
| Prometheus exceeds 160 MB | OOM-killed during a run | Drop high-cardinality series with relabel rules; if still over, swap in single-node VictoriaMetrics |
| repmgr failover takes longer than 30 s | E4 fails on RTO | Tune `reconnect_attempts` and `reconnect_interval`; record the measured value honestly if the target is still missed |
| Split-brain after a restart | Two nodes report `pg_is_in_recovery() = false` | The preflight check aborts the run; fix the entrypoint fencing rule before continuing |
| pgBackRest with a shared volume behaves badly after failover | Archive or backup errors on the new primary | Re-run `stanza-upgrade` in the promote hook; document the limitation |
| HAProxy retries cause duplicate writes | Duplicate rows or unique-key errors in logs | Idempotency keys already prevent duplicates; add a test that proves it |
| Hosted CI runner is slower than the laptop | Nightly targets flap | Keep one baseline per environment (`baseline.ci.json`, `baseline.local.json`) |
| 256 MB is too little for Postgres during the E5 seed | Postgres OOM-killed while seeding | Seed in batches of 10,000 rows; lower the seed size before raising the limit |
| WSL2 behaves differently from native Linux | Timing or networking differences | CI on `ubuntu-latest` is the reference environment |

### 12.3 Open decisions for the project owner

- [ ] GitHub repository name and owner, which also sets the Go module path.
- [ ] Development host: WSL2 or native Linux.
- [ ] Whether to register a domain for a stable tunnel URL, or use Quick Tunnels only.
- [ ] Whether to attempt the Oracle VM at all.

## 13. Instructions for the coding assistant

Paste this whole document into the assistant as the project brief; this section tells it how to work.

### 13.1 Working rules

1. Build one milestone at a time, in the order of section 11. State which milestone you are on at the start of each session.
2. Before writing code for a milestone, list the files you will create or change and wait for approval.
3. Do not add a service, dependency or tool that is not in section 4 without asking first and stating its memory cost.
4. Every Compose service must have `mem_limit`, `mem_reservation`, `pids_limit`, a healthcheck and a restart policy. A service without them is a defect.
5. Never invent a measurement. Numbers in the README, scorecards and commit messages come only from commands that were actually run, with the command shown.
6. When a target in section 8 is missed, report the measured value and the likely cause. Do not loosen the assertion to make it pass.
7. Check current official documentation for repmgr, pgBackRest, HAProxy, Toxiproxy and Prometheus before writing their configuration. Flags and defaults change between versions.
8. Prefer the standard library. Allowed Go dependencies: `pgx/v5`, `sony/gobreaker`, the OpenTelemetry SDK with its Prometheus exporter, a YAML parser, a Docker API client, and the Toxiproxy Go client. Ask before adding others.
9. Keep functions small and tested. Decision logic (retry, breaker, assertions, regression comparison, RTO and RPO calculation) must be pure functions with table-driven tests.
10. No secrets in code, examples, logs or test fixtures.
11. Write shell scripts with `set -euo pipefail` and pass ShellCheck.
12. When something in this document is ambiguous or contradictory, stop and ask. Section 2 wins any conflict.

### 13.2 Definition of done for every milestone

- [ ] All acceptance criteria for the milestone pass, with the commands and their output shown.
- [ ] `make test` and `ci.yml` are green.
- [ ] `make stats` shows every container under its limit.
- [ ] New behaviour has tests; new configuration is documented in the README or `docs/`.
- [ ] One commit, or a small series, with a message that names the milestone.

### 13.3 Code quality bar

- `gofmt`, `go vet` and golangci-lint clean, with no blanket `nolint`.
- Errors are wrapped with context and never silently discarded.
- Every network call takes a `context.Context` with a deadline.
- No global mutable state apart from metrics registration.
- Test coverage of `internal/` stays at 70% or higher.

### 13.4 First prompt to give the assistant

```text
You are building BlastRadius from the attached PRD. Read all of it first.
Then do only milestone M0: propose the file list, wait for my approval,
and create the skeleton. Follow section 13 exactly. Ask me the open
decisions in section 12.3 before you start.
```
