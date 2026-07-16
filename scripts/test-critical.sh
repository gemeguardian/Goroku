#!/usr/bin/env bash
# M9.4: mandatory critical test suites (race detector).
# Prefer TMPDIR=/root/.cache/go-tmp (or any large tmpfs) for -race.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

export TMPDIR="${TMPDIR:-/tmp}"
mkdir -p "$TMPDIR"

echo "==> critical: ./goroku/"
go test -race -count=1 ./goroku/

echo "==> critical: ./goroku/web/"
go test -race -count=1 ./goroku/web/

echo "==> critical: ./goroku/inline/"
go test -race -count=1 ./goroku/inline/

echo "==> critical: ./goroku/modules/ (may be long)"
go test -race -count=1 ./goroku/modules/

echo "critical suites ok"
