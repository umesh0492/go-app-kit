#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# Version & Documentation Synchronization Gate (go-app-kit)
# Guarantees that CHANGELOG.md, README.md, go.mod dependencies, and releases
# never drift out of sync.
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

CHANGELOG_VER=$(grep -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md | head -n1 | sed -E 's/## \[([0-9]+\.[0-9]+\.[0-9]+)\].*/\1/')
README_HEADER_VER=$(grep -E '^# go-app-kit · v' README.md | head -n1 | sed -E 's/# go-app-kit · v([0-9]+\.[0-9]+\.[0-9]+).*/\1/')
README_GET_VER=$(grep -E 'go get github.com/umesh0492/go-app-kit@v' README.md | head -n1 | sed -E 's/.*go-app-kit@v([0-9]+\.[0-9]+\.[0-9]+).*/\1/')
GOLIBS_DEP=$(grep 'github.com/umesh0492/go-libs' go.mod | grep -v replace | head -n1 | awk '{print $2}')

echo "========================================================"
echo "🔒 Verifying Version Synchronization (go-app-kit)"
echo "   - CHANGELOG.md:     v$CHANGELOG_VER"
echo "   - README.md Header: v$README_HEADER_VER"
echo "   - README.md go get: v$README_GET_VER"
echo "   - go-libs dep:      $GOLIBS_DEP"
echo "========================================================"

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

if grep -E '^\s*replace\s+' go.mod > /dev/null 2>&1; then
  echo "❌ Error: Forbidden 'replace' directive found in go.mod. Public releases must not contain local replace directives."
  exit 1
fi

echo "✅ All versions and dependencies are strictly bound and synchronized!"
