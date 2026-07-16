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
7. `scripts/scan-secrets.sh` (tracked tree only; M9.2)
8. `go test -race ./...`
9. Job `govulncheck-direct` (`GOVULNCHECK_DIRECT_ONLY=1`)

Soft / advisory:

- **govulncheck full scan** (pinned via `scripts/govulncheck.sh`, default `v1.1.4`) — main job stays **advisory** so stdlib / transitive noise does not block merge.
- **Coverage floor 20%** on critical packages — soft gate: warns and exits non-zero only if total drops below 20%; intended as a floor, not a quality target.

Hard gate (vulns):

- **govulncheck direct deps** — job `govulncheck-direct` runs `GOVULNCHECK_DIRECT_ONLY=1`. Uses `govulncheck -json` and fails only when the **vulnerable module** (first trace frame) is a direct `go.mod` require. **stdlib is always ignored.** Transitive modules (e.g. `golang.org/x/net`) do not fail this job.
- Optional full strict job `govulncheck-strict` on `workflow_dispatch` / weekly schedule (`GOVULNCHECK_STRICT=1`) — fails on any finding; not a PR merge gate.

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
| `govulncheck` advisory (pinned) | Done — main job |
| `govulncheck` direct-deps hard gate | Done — `govulncheck-direct` + `GOVULNCHECK_DIRECT_ONLY=1` |
| `govulncheck` full strict | Optional job (schedule / dispatch) |
| Secret scanning (tracked tree) | Done — `scripts/scan-secrets.sh` in main CI job |
| Host GitHub secret scanning / gitleaks org policy | **Residual** (repo-host feature; still do not commit runtime secrets) |
| SBOM generation | **Required in CI:** `bash scripts/generate-sbom.sh` → CycloneDX `sbom.cdx.json` uploaded as `sbom` artifact. **Optional:** pinned Syft (`SYFT_VERSION`, continue-on-error) uploads `sbom-syft` when install succeeds. Cosign remains optional local (`COSIGN_YES=1`) |
| Dependency review action on PRs | **Residual** |
| License policy automation | **Residual** |
| Mass `gotd/td` upgrade | **Out of scope** — see `docs/RELEASE.md` pin notes |

Local:

```bash
bash scripts/scan-secrets.sh                             # tracked-tree secret scan
bash scripts/govulncheck.sh                              # advisory full
GOVULNCHECK_DIRECT_ONLY=1 bash scripts/govulncheck.sh    # fail on direct-dep vulns only
GOVULNCHECK_STRICT=1 bash scripts/govulncheck.sh         # fail on any finding
bash scripts/generate-sbom.sh dist/sbom                  # prints SBOM_ARTIFACT_PATH=
bash scripts/release-check.sh                            # M10 pre-release + canary checklist
```

Known class of findings: Go stdlib fixes often require a **Go patch bump** in `go.mod` / runners; transitive `x/net` / compress bumps should be small, intentional PRs — not bundled with product refactors or gotd upgrades.
