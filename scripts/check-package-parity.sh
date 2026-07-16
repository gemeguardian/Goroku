#!/usr/bin/env bash
# M9.1: ensure package composition matches a clean checkout.
# 1) No gitignored .go files under package directories (Go still compiles them).
# 2) Every package Go/Test file is tracked by git when inside a git worktree.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

fail=0

# Runtime / sample trees that must never participate in product package checks.
skip_path() {
  case "$1" in
    .git/*|.goroku_go/*|.goroku_plugins/*|modules/*|user_modules/*|./.git/*|./.goroku_go/*|./.goroku_plugins/*|./modules/*|./user_modules/*)
      return 0
      ;;
  esac
  return 1
}

# Present but gitignored .go files change local package composition.
while IFS= read -r -d '' f; do
  rel="${f#./}"
  if skip_path "$rel"; then
    continue
  fi
  if git check-ignore -q "$rel" 2>/dev/null; then
    echo "error: gitignored Go source is present under a package path: $rel" >&2
    echo "  move it out of the package directory or track it intentionally" >&2
    fail=1
  fi
done < <(find . -name '*.go' \
  -not -path './.git/*' \
  -not -path './.goroku_go/*' \
  -not -path './.goroku_plugins/*' \
  -not -path './modules/*' \
  -not -path './user_modules/*' \
  -print0)

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "warning: not a git worktree; skipping tracked-file check" >&2
  exit "$fail"
fi

tracked="$(git ls-files -- '*.go')"
is_tracked() {
  printf '%s\n' "$tracked" | grep -Fxq "$1"
}

# Every file Go would compile for product packages must be tracked.
while IFS= read -r pkg; do
  [ -n "$pkg" ] || continue
  dir="$(go list -f '{{.Dir}}' "$pkg")"
  rel_dir="${dir#$root/}"
  if skip_path "$rel_dir" || skip_path "${rel_dir}/"; then
    continue
  fi
  # Nested modules under the tree (e.g. user_modules/) should not appear; belt-and-suspenders.
  case "$pkg" in
    */user_modules|*/user_modules/*|goroku_user_modules|goroku_user_modules/*) continue ;;
  esac
  for field in GoFiles TestGoFiles XTestGoFiles; do
    files="$(go list -f "{{range .$field}}{{println .}}{{end}}" "$pkg")"
    while IFS= read -r base; do
      [ -n "$base" ] || continue
      abs="$dir/$base"
      rel="${abs#$root/}"
      if skip_path "$rel"; then
        continue
      fi
      if ! is_tracked "$rel"; then
        echo "error: package $pkg compiles untracked file: $rel" >&2
        fail=1
      fi
    done <<< "$files"
  done
done < <(go list ./...)

if [ "$fail" -ne 0 ]; then
  echo "package parity check failed" >&2
  exit 1
fi

echo "package parity ok"
