#!/usr/bin/env bash
# Pinned govulncheck helper (M9.2).
#
# Modes:
#   default                  — full human report; advisory (exit 0) so CI is not
#                              blocked by stdlib / transitive noise
#   GOVULNCHECK_STRICT=1     — fail on any finding (full scan)
#   GOVULNCHECK_DIRECT_ONLY=1— -json filter: fail only when the vulnerable module
#                              is a direct go.mod require (stdlib always ignored)
#
# Do not mass-upgrade gotd/td from this script.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PINNED="${GOVULNCHECK_VERSION:-v1.1.4}"
STRICT="${GOVULNCHECK_STRICT:-0}"
DIRECT_ONLY="${GOVULNCHECK_DIRECT_ONLY:-0}"

echo "Installing govulncheck@${PINNED}"
go install "golang.org/x/vuln/cmd/govulncheck@${PINNED}"
GV="$(go env GOPATH)/bin/govulncheck"
echo "Using ${GV} (pinned ${PINNED})"

if [[ "$DIRECT_ONLY" == "1" ]]; then
  echo "govulncheck: direct-module-deps filter (stdlib ignored)"
  work="${TMPDIR:-/tmp}/govulncheck-direct-$$"
  mkdir -p "$work"
  json_out="${work}/govuln.json"
  direct_list="${work}/direct-mods.txt"

  main_mod="$(go list -m -f '{{.Path}}')"
  go list -m -f '{{if not .Indirect}}{{.Path}}{{end}}' all \
    | sed '/^$/d' \
    | grep -vxF "$main_mod" >"$direct_list" || true

  set +e
  "$GV" -json ./... >"$json_out"
  raw_code=$?
  set -e

  set +e
  python3 - "$json_out" "$direct_list" <<'PY'
import json
import sys

json_path, direct_path = sys.argv[1], sys.argv[2]
direct = {line.strip() for line in open(direct_path, encoding="utf-8") if line.strip()}
data = open(json_path, encoding="utf-8").read()
decoder = json.JSONDecoder()
idx = 0
findings = []
while idx < len(data):
    while idx < len(data) and data[idx].isspace():
        idx += 1
    if idx >= len(data):
        break
    obj, end = decoder.raw_decode(data, idx)
    idx = end
    if not isinstance(obj, dict) or "finding" not in obj:
        continue
    f = obj["finding"]
    vuln_mod = None
    for fr in f.get("trace") or []:
        if fr.get("module"):
            vuln_mod = fr["module"]
            break
    # First frame is the vulnerable module/symbol (govulncheck convention).
    if not vuln_mod or vuln_mod == "stdlib":
        continue
    if vuln_mod not in direct:
        continue
    findings.append((f.get("osv", "?"), vuln_mod, f.get("fixed_version", "")))

uniq = sorted(set(findings))
if not uniq:
    print("govulncheck-direct: clean (no vulns in direct module deps)")
    print(f"direct modules scanned: {len(direct)}")
    sys.exit(0)

print("govulncheck-direct: findings in direct module dependencies:")
for osv, mod, fixed in uniq:
    extra = f" (fixed in {fixed})" if fixed else ""
    print(f"  {osv}  module={mod}{extra}")
print(f"total unique: {len(uniq)}")
sys.exit(1)
PY
  code=$?
  set -e
  rm -rf "$work"

  if [[ "$code" -eq 0 ]]; then
    exit 0
  fi
  echo "govulncheck-direct: failing on direct-dep vulns (raw scan exit was ${raw_code})"
  exit "$code"
fi

# Full human-readable scan (advisory or strict).
set +e
"$GV" ./...
code=$?
set -e

if [[ "$code" -eq 0 ]]; then
  echo "govulncheck: clean"
  exit 0
fi

echo "govulncheck: findings reported (exit ${code})"
if [[ "$STRICT" == "1" ]]; then
  echo "GOVULNCHECK_STRICT=1 — failing"
  exit "$code"
fi
echo "Advisory mode — not failing CI (set GOVULNCHECK_STRICT=1 or GOVULNCHECK_DIRECT_ONLY=1 for hard gates)"
exit 0
