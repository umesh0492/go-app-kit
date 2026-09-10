#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# Version & Documentation Truth Synchronization Gate (go-app-kit)
# Enforces:
# 1. Version lock: README header == README go get == CHANGELOG top == git tag
# 2. Ban on 'replace' directives in go.mod
# 3. Ban on wrong module versions (@v<major>, @v1.5.0, etc.) in docs
# 4. Ban on unsupported Go language versions (< Go 1.25) in docs
# 5. CHANGELOG exported symbol inventory verification
# 6. Volatile numbers verification (package count, global and per-package coverage %)
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

CHANGELOG_VER=$(grep -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | head -n1 | sed -E 's/## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/')
README_HEADER_VER=$(grep -E '^# go-app-kit · v' README.md | head -n1 | sed -E 's/# go-app-kit · v([0-9]+\.[0-9]+\.[0-9]+).*/\1/')
README_GET_VER=$(grep -E 'go get github.com/umesh0492/go-app-kit@v' README.md | head -n1 | sed -E 's/.*go-app-kit@v([0-9]+\.[0-9]+\.[0-9]+).*/\1/')
GOLIBS_DEP=$(grep 'github.com/umesh0492/go-libs' go.mod | grep -v replace | head -n1 | awk '{print $2}')

echo "========================================================"
echo "🔒 Verifying Version & Documentation Truth (go-app-kit)"
echo "   - CHANGELOG.md:     v$CHANGELOG_VER"
echo "   - README.md Header: v$README_HEADER_VER"
echo "   - README.md go get: v$README_GET_VER"
echo "   - go-libs dep:      $GOLIBS_DEP"
echo "========================================================"

# --- 1. Version Lock ---
if [ -z "$CHANGELOG_VER" ]; then
  echo "❌ Error: Unable to determine release version from CHANGELOG.md"
  exit 1
fi

if [ "$CHANGELOG_VER" != "$README_HEADER_VER" ]; then
  echo "❌ Error: Version mismatch between CHANGELOG.md (v$CHANGELOG_VER) and README.md header (v$README_HEADER_VER)"
  exit 1
fi

if [ "$CHANGELOG_VER" != "$README_GET_VER" ]; then
  echo "❌ Error: Version mismatch between CHANGELOG.md (v$CHANGELOG_VER) and README.md go get (v$README_GET_VER)"
  exit 1
fi

if [ "$CHANGELOG_VER" != "0.1.0" ]; then
  echo "❌ Error: Expected repository version to be 0.1.0, but got $CHANGELOG_VER"
  exit 1
fi

if [ "$GOLIBS_DEP" != "v0.1.0" ]; then
  echo "❌ Error: Expected go-libs dependency to be v0.1.0, but got $GOLIBS_DEP"
  exit 1
fi

# Tag check: if on a git tag build or GIT_TAG is set
RESOLVED_TAG="${GIT_TAG:-}"
if [ -z "$RESOLVED_TAG" ] && [ "${GITHUB_REF_TYPE:-}" = "tag" ]; then
  RESOLVED_TAG="${GITHUB_REF_NAME:-}"
fi
if [ -z "$RESOLVED_TAG" ]; then
  RESOLVED_TAG=$(git describe --tags --exact-match 2>/dev/null || true)
fi

if [ -n "$RESOLVED_TAG" ]; then
  CLEAN_TAG="${RESOLVED_TAG#v}"
  if [ "$CLEAN_TAG" != "$CHANGELOG_VER" ]; then
    echo "❌ Error: Git tag ($RESOLVED_TAG) does not match CHANGELOG version (v$CHANGELOG_VER)"
    exit 1
  fi
  echo "   - Git tag:          v$CLEAN_TAG (matched)"
fi

# --- 2. Ban on 'replace' directives in go.mod ---
if grep -E '^\s*replace\s+' go.mod > /dev/null 2>&1; then
  echo "❌ Error: Forbidden 'replace' directive found in go.mod. Public releases must not contain local replace directives."
  exit 1
fi

# --- 3. Ban on wrong module versions (@v<major>, @v1.5.0, etc.) in docs ---
# Find all markdown files in repo
DOC_FILES=(README.md CHANGELOG.md)
while IFS= read -r f; do
  [ -f "$f" ] && DOC_FILES+=("$f")
done < <(find docs */README.md -type f -name "*.md" 2>/dev/null || true)

WRONG_VERSIONS=$(grep -rnE '@v[0-9]+(\.[0-9]+)*' "${DOC_FILES[@]}" 2>/dev/null | grep -v "@v${CHANGELOG_VER}" || true)
if [ -n "$WRONG_VERSIONS" ]; then
  echo "❌ Error: Mismatched or unsupported @v version tag found in documentation (must be @v${CHANGELOG_VER}):"
  echo "$WRONG_VERSIONS"
  exit 1
fi

# --- 4. Ban on unsupported Go language versions (< Go 1.25) in docs ---
UNSUPPORTED_GO=$(grep -rniE '\bgo\s*(\([a-z]+\)\s*)?(>=|>)?\s*1\.(1[0-9]|2[0-4])\b' "${DOC_FILES[@]}" 2>/dev/null || true)
if [ -n "$UNSUPPORTED_GO" ]; then
  echo "❌ Error: Documentation mentions unsupported Go language version (< Go 1.25):"
  echo "$UNSUPPORTED_GO"
  exit 1
fi

# --- 5. Exported Symbol Inventory Verification for CHANGELOG ---
if [ -f "${SCRIPT_DIR}/verify_changelog_symbols.sh" ]; then
  bash "${SCRIPT_DIR}/verify_changelog_symbols.sh"
else
  echo "❌ Error: ${SCRIPT_DIR}/verify_changelog_symbols.sh not found!"
  exit 1
fi

# --- 6. Volatile Numbers Verification (Package Count & Coverage %) ---
echo "🔢 Verifying volatile numbers against measured toolchain output..."

# A. Package Count
ACTUAL_PKG_COUNT=$(find . -maxdepth 1 -mindepth 1 -type d ! -name '.*' ! -name 'docs' ! -name 'examples' ! -name 'scripts' | while read -r d; do if ls "$d"/*.go >/dev/null 2>&1; then echo "$d"; fi; done | wc -l | tr -d ' ')
README_PKG_HEADINGS=$(grep -E '^### [0-9]+\. ' README.md | wc -l | tr -d ' ')

if [ "$ACTUAL_PKG_COUNT" -ne 6 ]; then
  echo "❌ Error: Unexpected core package count in repo: $ACTUAL_PKG_COUNT (expected 6)"
  exit 1
fi

if [ "$README_PKG_HEADINGS" -ne "$ACTUAL_PKG_COUNT" ]; then
  echo "❌ Error: README lists $README_PKG_HEADINGS package modules, but repository contains $ACTUAL_PKG_COUNT core packages"
  exit 1
fi

# Check any explicit package count claim in README (e.g. "N packages" or "N modules")
WRONG_PKG_CLAIMS=$(grep -nE '\b[0-9]+\s+(core\s+)?packages\b' README.md 2>/dev/null | grep -v "\b${ACTUAL_PKG_COUNT}\s+" || true)
if [ -n "$WRONG_PKG_CLAIMS" ]; then
  echo "❌ Error: README contains incorrect package count claim:"
  echo "$WRONG_PKG_CLAIMS"
  exit 1
fi

# B. Statement Coverage Verification
trap 'rm -f /tmp/gak_check_cov.out' EXIT
PER_PKG_TEST_OUT=$(go test -coverprofile=/tmp/gak_check_cov.out ./...)
MEASURED_GLOBAL_COV_STR=$(go tool cover -func=/tmp/gak_check_cov.out | grep total | awk '{print $3}')
MEASURED_GLOBAL_COV=$(echo "${MEASURED_GLOBAL_COV_STR}" | tr -d '%')

# Global coverage in README callout
README_GLOBAL_COV=$(grep -oE 'Overall Repository Statement Coverage: [0-9]+\.[0-9]+%' README.md | head -n1 | grep -oE '[0-9]+\.[0-9]+' || true)
if [ -z "$README_GLOBAL_COV" ]; then
  echo "❌ Error: Could not locate 'Overall Repository Statement Coverage' in README.md"
  exit 1
fi

if [ "$README_GLOBAL_COV" != "$MEASURED_GLOBAL_COV" ]; then
  echo "❌ Error: Stated global coverage in README.md (${README_GLOBAL_COV}%) does not match measured toolchain coverage (${MEASURED_GLOBAL_COV}%)"
  exit 1
fi

# Global coverage in README table total row
README_TABLE_TOTAL=$(grep -E '\|\s*\*\*Total Statement Coverage\*\*\s*\|' README.md | grep -oE '[0-9]+\.[0-9]+' | tail -n1 || true)
if [ -z "$README_TABLE_TOTAL" ]; then
  echo "❌ Error: Could not locate 'Total Statement Coverage' row in README.md"
  exit 1
fi

if [ "$README_TABLE_TOTAL" != "$MEASURED_GLOBAL_COV" ]; then
  echo "❌ Error: Stated table total coverage in README.md (${README_TABLE_TOTAL}%) does not match measured toolchain coverage (${MEASURED_GLOBAL_COV}%)"
  exit 1
fi

# Per-package coverage verification
TABLE_ROWS=$(sed -n '/## 📊 Verified Statement Coverage Status/,/## Verification & Quality Gates/p' README.md | grep -E '^\|\s*`?[a-zA-Z0-9_/]+`?\s*\|' | grep -v 'Package' | grep -v 'Total Statement Coverage' || true)

while IFS= read -r row; do
  [ -z "$row" ] && continue
  PKG=$(echo "$row" | awk -F'|' '{print $2}' | tr -d '`' | tr -d ' ')
  STATED_COV=$(echo "$row" | grep -oE '\*\*[0-9]+\.[0-9]+%\*\*' | tr -d '*' | tr -d '%' || true)
  
  if [ -n "$PKG" ] && [ -n "$STATED_COV" ]; then
    MEASURED_PKG_COV=$(echo "${PER_PKG_TEST_OUT}" | grep -E "(^|[[:space:]/])${PKG}[[:space:]]" | grep -oE 'coverage: [0-9]+\.[0-9]+%' | grep -oE '[0-9]+\.[0-9]+' | head -n1 || true)
    if [ -n "$MEASURED_PKG_COV" ]; then
      if [ "$STATED_COV" != "$MEASURED_PKG_COV" ]; then
        echo "❌ Error: Stated coverage for package '$PKG' in README.md (${STATED_COV}%) does not match measured coverage (${MEASURED_PKG_COV}%)"
        exit 1
      fi
      echo "   - Verified package coverage for '$PKG': ${STATED_COV}%"
    fi
  fi
done <<< "${TABLE_ROWS}"

echo "========================================================"
echo "✅ All versions, symbols, and metrics strictly verified!"
echo "========================================================"
exit 0
