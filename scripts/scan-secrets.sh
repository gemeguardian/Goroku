#!/usr/bin/env bash
# M9.2: scan tracked git tree only for likely secrets / secret filenames.
# Fails non-zero if a high-confidence pattern matches. Does not scan untracked
# runtime files (config.json, sessions, logs) — those must stay gitignored.
#
# Usage:
#   bash scripts/scan-secrets.sh
#   SCAN_SECRETS_VERBOSE=1 bash scripts/scan-secrets.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "scan-secrets: not a git worktree; refusing to scan arbitrary trees" >&2
  exit 2
fi

VERBOSE="${SCAN_SECRETS_VERBOSE:-0}"
fail=0
hits=0

# Known secret / credential filenames that must never be tracked.
# Patterns are matched against the full tracked path (basename-aware).
FORBIDDEN_NAME_REGEX='(^|/)\.env($|\.)|(^|/)id_rsa$|(^|/)id_ed25519$|(^|/)\.aws/credentials$|(^|/)credentials\.json$|(^|/)service[-_]account\.json$|(^|/)google[-_]application[-_]credentials\.json$|\.pem$|\.p12$|\.pfx$|(^|/)goroku-.*\.session$|(^|/)config-[0-9]+\.json$'

# High-confidence content patterns (ERE). Intentionally narrow to limit noise.
# 1) PEM / OpenSSH private keys
# 2) AWS access key IDs
# 3) Telegram Bot API tokens (digits:base64-ish)
# 4) GitHub classic / fine-grained PATs
# 5) Generic "api_hash"/"bot_token" assignments with long literal values in non-test docs
CONTENT_PATTERNS=(
  '-----BEGIN ([A-Z0-9 ]+)?PRIVATE KEY-----'
  '-----BEGIN OPENSSH PRIVATE KEY-----'
  'AKIA[0-9A-Z]{16}'
  '(^|[^0-9A-Za-z])[0-9]{8,10}:[A-Za-z0-9_-]{35}([^0-9A-Za-z]|$)'
  'ghp_[A-Za-z0-9]{36}'
  'github_pat_[A-Za-z0-9_]{20,}'
  'xox[baprs]-[A-Za-z0-9-]{10,}'
)

# Paths we never flag as content hits (tests, docs examples, vendored hashes).
# Name-based forbidden paths are still checked for every tracked file.
CONTENT_SKIP_REGEX='(^|/)(go\.sum|LICENSE|.*_test\.go|.*\.md|langpacks/|docs/|scripts/scan-secrets\.sh)$'

mapfile -t TRACKED < <(git ls-files -z | tr '\0' '\n' | sed '/^$/d')

if [[ ${#TRACKED[@]} -eq 0 ]]; then
  echo "scan-secrets: no tracked files"
  exit 0
fi

report() {
  local kind="$1" path="$2" detail="$3"
  hits=$((hits + 1))
  fail=1
  echo "SECRET_HIT kind=${kind} path=${path} ${detail}"
}

echo "scan-secrets: checking ${#TRACKED[@]} tracked paths"

for path in "${TRACKED[@]}"; do
  # --- forbidden filenames ---
  if echo "$path" | grep -Eiq "$FORBIDDEN_NAME_REGEX"; then
    report "filename" "$path" "reason=forbidden_secret_filename"
    continue
  fi

  # Skip missing / empty / binary-ish for content scan
  if [[ ! -f "$path" ]] || [[ ! -s "$path" ]]; then
    continue
  fi
  if file -b --mime-encoding "$path" 2>/dev/null | grep -qi 'binary'; then
    # Still flag private-key-looking PEM if file(1) mislabels; try text grep only on text.
    continue
  fi
  if echo "$path" | grep -Eq "$CONTENT_SKIP_REGEX"; then
    [[ "$VERBOSE" == "1" ]] && echo "skip-content path=${path}"
    continue
  fi

  for pat in "${CONTENT_PATTERNS[@]}"; do
    # grep -n for line context; limit to first hit per pattern per file
    if line="$(grep -nE "$pat" -- "$path" 2>/dev/null | head -n 1)"; then
      lineno="${line%%:*}"
      report "content" "$path" "line=${lineno} pattern=${pat}"
      break
    fi
  done
done

# High-entropy base64-ish blobs on assignment lines (optional second pass, tracked text only).
# Catches long random secrets assigned to common key names without matching fixed formats.
ENTROPY_NAME_REGEX='(api[_-]?hash|api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token|bot[_-]?token|private[_-]?key)\s*[=:]\s*["'\'']?[A-Za-z0-9+/=_\-]{32,}'
for path in "${TRACKED[@]}"; do
  if [[ ! -f "$path" ]] || [[ ! -s "$path" ]]; then
    continue
  fi
  if echo "$path" | grep -Eq "$CONTENT_SKIP_REGEX"; then
    continue
  fi
  if echo "$path" | grep -Eiq "$FORBIDDEN_NAME_REGEX"; then
    continue
  fi
  if line="$(grep -nEi "$ENTROPY_NAME_REGEX" -- "$path" 2>/dev/null | head -n 1)"; then
    # Allow obvious placeholders / empty-looking examples
    if echo "$line" | grep -Eiq 'changeme|your[_-]?token|example|placeholder|xxx+|TODO|FIXME|dummy|test.?token|0{8,}'; then
      continue
    fi
    lineno="${line%%:*}"
    report "entropy-assign" "$path" "line=${lineno}"
  fi
done

if [[ "$fail" -ne 0 ]]; then
  echo "scan-secrets: FAILED (${hits} hit(s) in tracked tree)"
  echo "Remove secrets from git history/index; rotate per SECURITY.md if real credentials leaked."
  exit 1
fi

echo "scan-secrets: clean (tracked tree)"
exit 0
