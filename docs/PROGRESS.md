## M0 (attempt 1)
- Done: skeleton, Go module, stub cmds with one table test, Makefile, .env.example, MIT licence, compose with 12 busybox placeholder services (limits 1088 MB always-on, 1328 MB total), ci.yml, .golangci.yml.
- Not verified: CI on GitHub, containers actually starting, SHA-pinned actions, real image tags (docs/QUESTIONS.md).
- golangci-lint is installed at ~/go/bin; Makefile falls back to it.
- Next: M1 API (needs real Postgres image tag; verify versions first).
- Untracked archive-*/ dirs (gk zip) are git-ignored, not mine.

## M1 (attempt 1)
- Done: API (all 6.1 endpoints except M3 resilience), embedded migrations + `migrate` service under advisory lock, HAProxy/Toxiproxy/pg1 wired, scratch non-root API image (20.8 MB), k6 script, smoke script, unit + integration tests, evidence in docs/evidence/M1.md.
- Measured: k6 30 RPS x 5 min -> p99 8.58 ms, 0/9000 failed. Coverage api 93.0%, store 82.9%. HAProxy uses 80.8% of its 32 MB limit, Toxiproxy 74.5%.
- Not verified: GitHub CI, hadolint, replica lag (no replica yet), HAProxy POST-retry behaviour (QUESTIONS 5).
- Next: M2 replication. Needs repmgr/pgBackRest image + own pg_hba.conf (QUESTIONS 9); read repmgr 5.5 docs first; pg2 is still a busybox placeholder.

## M2 (attempt 1, finished over two sessions)
- Done: custom Postgres image (postgres:17.12-bookworm + repmgr 5.5.0 + pgBackRest 2.60.0), entrypoint with the fencing rule, pg2 streaming standby with a physical slot, repmgrd automatic failover, `make pgstatus` / `failover-drill` / `stale-primary-drill` / `reclone`, runbook, ADRs 0001 and 0002, evidence in docs/evidence/M2.md.
- Measured (by hand, no load, 3 drills): promotion 11.6 to 13.4 s after SIGKILL, rejoin 4.8 to 5.3 s, never two primaries seen. k6 30 RPS x 5 min with replication: p99 8.35 ms, 0/9001 failed, replay lag about 2 ms.
- Review (database-reviewer, security-reviewer) found and this session fixed: a stale primary booting first made the newer node rewind itself (data loss); initdb on a wiped but used volume; half-finished rewind startable as primary; superuser logins open to every container; passwords on psql command lines. Details: QUESTIONS 18 to 23.
- Changed under your feet: PGDATA is now /var/lib/postgresql/data/pgdata with marker files beside it; pg1/pg2 have fixed IPs .201/.202 (pg_hba names them); `make up` needs `.env`; base tag went 17.9 -> 17.12 (the 17.9 tag was already running 17.12 binaries).
- Not verified: CI on GitHub, hadolint, kill in the middle of pg_rewind/clone, network partition. go-reviewer not run (6-line Go change).
- Watch in later milestones: HAProxy sits at 85% of its 32 MB limit (M4 gate is 90%). Passwords are visible via `docker inspect` once dockerproxy exists (QUESTIONS 20, decide in M4). In the stale-primary case two primaries coexist for up to 2 s (QUESTIONS 22); M4 preflight should poll, not sample once. archive_command is still /bin/true (M5). pgBackRest restore in M5 must target the pgdata subdirectory and can remove the volume (markers go with it).
- Next: M3 resilience in the API (retries, breaker, read-only mode); the manual check there is `make failover-drill` under load.
