#!/usr/bin/env bash
# Pinned govulncheck helper (M9.2).
# Default: advisory (exit 0 after printing findings) so CI is not blocked by
# known stdlib / transitive noise that needs deliberate Go/x/* bumps.
# Strict: GOVULNCHECK_STRICT=1 fails on any finding.
#
# Do not mass-upgrade gotd/td from this script.
set -euo pipefail

PINNED="${GOVULNCHECK_VERSION:-v1.1.4}"
STRICT="${GOVULNCHECK_STRICT:-0}"

echo "Installing govulncheck@${PINNED}"
go install "golang.org/x/vuln/cmd/govulncheck@${PINNED}"
GV="$(go env GOPATH)/bin/govulncheck"

"$GV" -show version ./...
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
echo "Advisory mode — not failing CI (set GOVULNCHECK_STRICT=1 for hard gate)"
exit 0
