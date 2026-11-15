# Pre-answered decisions (PRD 12.3). Fill the <> before the first run.

- GitHub owner/repo: Adarsh16-30/BlastRadius. Go module path: github.com/Adarsh16-30/BlastRadius
- Development host: WSL2. If WSL2, assume .wslconfig as in PRD section 2.
- Tunnel: Quick Tunnels only, no domain.
- Oracle VM / infra/: skip. Create infra/README.md with a one-line "not implemented" note only.
- Licence: MIT. Author in docs: Adarsh Tripathi.
- Image versions: pin per PRD section 4 version policy, after checking official docs at build time.
- Run mode: unattended, owner reviews at the end.
- Go toolchain: go1.27.4 (current stable at setup, installed locally). Use it for go.mod, Dockerfile builder images and CI setup-go; re-check official docs per PRD section 4 version policy.
- Docker/GHCR image names and compose project name must be lowercase (e.g. ghcr.io/adarsh16-30/blastradius-api).
