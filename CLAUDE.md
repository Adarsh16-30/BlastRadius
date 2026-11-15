# BlastRadius: autonomous build mode

Spec (source of truth): @docs/PRD.md
Pre-answered decisions: @docs/DECISIONS.md
Handoff log: docs/PROGRESS.md (read at start, append at end)

## Overrides to PRD section 13 (owner reviews at the end, not per step)
- 13.1.2 "wait for approval": write the file list to docs/plans/M<N>.md, then proceed.
- 12.3 / 13.1.12 "ask": answers are in docs/DECISIONS.md. Anything still unanswered: log it in docs/QUESTIONS.md, choose the option that best satisfies PRD section 2, continue.
- Never ask the user a question mid-run. The session is non-interactive.
- Still in force: section 2 wins conflicts, no invented measurements, no loosened assertions, no unlisted service or dependency (13.1.3, 13.1.8).
- Evidence: every acceptance criterion is proven in docs/evidence/M<N>.md as `command` + real output pasted. A criterion without evidence is not done.
- If blocked: write docs/BLOCKED.md (cause, measured values, what was tried) and stop. Delete it only after fixing the cause.

## Routing (use these; do not browse the whole catalogue)
Flow per milestone: planner -> tdd-workflow -> implement -> go-reviewer + code-reviewer (fresh context) -> fix -> verification-loop.
Invoke agents/skills by name; headless mode has no slash commands.
Before writing config for repmgr, pgBackRest, HAProxy, Toxiproxy, Prometheus, Grafana: use search-first / docs-lookup, read the current official docs, put doc URL + version in a config comment (PRD 13.1.7, section 4 version policy).
Build breaks: go-build-resolver / build-error-resolver.

| M | Primary skills / agents |
|---|---|
| M0 skeleton | planner, docker-patterns, deployment-patterns |
| M1 API | golang-patterns, golang-testing, api-design, database-migrations, postgres-patterns |
| M2 replication | architect, postgres-patterns, docker-patterns, database-reviewer, security-reviewer, search-first |
| M3 resilience | golang-patterns, golang-testing, tdd-workflow (pure funcs, table-driven) |
| M4 chaos runner | golang-patterns, golang-testing, go-reviewer, security-reviewer (Docker API, dockerproxy) |
| M5 backup/restore | postgres-patterns, database-reviewer, search-first |
| M6 observability | docs-lookup (Prometheus, Grafana), docker-patterns |
| M7 automation | deployment-patterns, security-review, doc-updater |

Also run security-reviewer on any change under deploy/ or .github/ in any milestone.

## Ponytail
Level: full. On conflict the PRD wins:
- Decision logic needs table-driven tests (13.1.9), not "one small test".
- Coverage of internal/ >= 70% (PRD 13.3), not ECC's 80% default.
- Never cut validation, security hardening (PRD section 9), or timeouts/contexts.
Mark deliberate shortcuts with `shortcut:` comments.

## Hard limits (PRD section 2, restated)
Every Compose service: mem_limit, mem_reservation, pids_limit, healthcheck, restart policy. $0. Docker Compose only. Total always-on <= 1.1 GB.
