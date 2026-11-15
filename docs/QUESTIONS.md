# Open questions

1. **Image versions unverified.** Only web-search results were available (network tools needed approval). Targets: HAProxy 3.4.7, Prometheus 3.15.1, Grafana OSS 13.3.0, k6 2.1.0. Toxiproxy, docker-socket-proxy, cloudflared and postgres 17.x tags were not found. M0 runs busybox:1.37.0 placeholders only; verify real tags against registries in the milestone that swaps each image in.
2. **GitHub Actions not SHA-pinned** (PRD 9). Chosen: major tags in M0, pin in M7.
3. **golangci-lint** was installed with `go install` into the Go bin dir; CI uses the official action.
