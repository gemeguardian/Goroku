# CI, coverage, and security checks (M9.2–M9.4)

## Workflow

GitHub Actions: `.github/workflows/ci.yml` (Go CI).

Hard gates (must pass):

1. `gofmt` on main-module package sources
2. `go mod verify` + `go mod tidy` diff
3. `scripts/check-package-parity.sh` (M9.1)
4. `go vet ./...`
5. `golangci-lint` (pinned version)
6. `go build` main binary
7. `go test -race ./...`

Soft / advisory:

- **govulncheck** (pinned `golang.org/x/vuln/cmd/govulncheck`) — reports known vulns; does not fail the build while residual stdlib / transitive findings are tracked (see below).
- **Coverage floor 20%** on critical packages — soft gate: warns and exits non-zero only if total drops below 20%; intended as a floor, not a quality target.

## Coverage policy (M9.3)

Do **not** chase a global 40% gate with cheap tests. Prefer critical-path coverage.

| Scope | Floor (now) | Target (alpha) | Target (stable) |
|-------|-------------|----------------|-----------------|
| Overall project | ~20% soft floor in CI | ≥ 25% | ≥ 40% |
| Persistence / DB state machine | measure | ≥ 80% key files | ≥ 80% |
| Command registry / security routing | measure | ≥ 90% main branches | ≥ 90% |
| Backup validation / apply | measure | ≥ 80% | ≥ 80% |
| Web auth / session | measure | ≥ 80% | ≥ 80% |
| Lifecycle / shutdown | scenario tests required | scenario + race | same |

Local coverage sample:

```bash
export TMPDIR=/root/.cache/go-tmp   # or any large temp dir for -race
go test -count=1 -coverprofile=coverage.out \
  ./goroku/ ./goroku/web/ ./goroku/inline/ ./goroku/utils/ ./goroku/cache/
go tool cover -func=coverage.out | tail -1
```

## Mandatory test suites (M9.4)

```bash
bash scripts/test-critical.sh
# equivalent:
# go test -race -count=1 ./goroku/ ./goroku/web/ ./goroku/inline/ ./goroku/modules/
```

Also keep M9.1 parity + full race suite green:

```bash
bash scripts/check-package-parity.sh
go test -race ./...
```

## M9.2 residual (not fully automated yet)

| Check | Status |
|-------|--------|
| `govulncheck` in CI (pinned) | Done (advisory until Go/x deps bumps are deliberate) |
| Secret scanning (gitleaks / GitHub secret scanning) | **Residual** — enable on the host repo; do not commit runtime secrets |
| SBOM generation (e.g. Syft / `govulncheck` companion) | **Residual** — planned with release pipeline (M10) |
| Dependency review action on PRs | **Residual** |
| License policy automation | **Residual** |
| Mass `gotd/td` upgrade | **Out of scope for M9.2** — separate milestone |

Known class of findings: Go stdlib fixes often require a **Go patch bump** in `go.mod` / runners; transitive `x/net` / compress bumps should be small, intentional PRs — not bundled with product refactors.
