## M0 (attempt 1)
- Done: skeleton, Go module, stub cmds with one table test, Makefile, .env.example, MIT licence, compose with 12 busybox placeholder services (limits 1088 MB always-on, 1328 MB total), ci.yml, .golangci.yml.
- Not verified: CI on GitHub, containers actually starting, SHA-pinned actions, real image tags (docs/QUESTIONS.md).
- golangci-lint is installed at ~/go/bin; Makefile falls back to it.
- Next: M1 API (needs real Postgres image tag; verify versions first).
- Untracked archive-*/ dirs (gk zip) are git-ignored, not mine.
