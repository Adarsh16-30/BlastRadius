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
