#!/usr/bin/env bash
set -euo pipefail

# Scripts directory and project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

GLOBAL_FLOOR=88.0
PACKAGE_FLOOR=75.0

echo "========================================================"
echo "🛡️ Running Comprehensive Statement Coverage Gate (go-app-kit)"
echo "   - Global Floor:       >= ${GLOBAL_FLOOR}%"
echo "   - Per-Package Floor:  >= ${PACKAGE_FLOOR}%"
echo "========================================================"

COVERAGE_FILE="${1:-coverage.out}"
SUMMARY_FILE="/tmp/gak_coverage_packages.txt"

if [ -f "${COVERAGE_FILE}" ] && [ -s "${COVERAGE_FILE}" ] && [ -f "${SUMMARY_FILE}" ]; then
    echo "==> Reusing existing coverage profile: ${COVERAGE_FILE}"
    PER_PKG_OUTPUT=$(cat "${SUMMARY_FILE}")
else
    echo "==> Generating coverage profile in single test suite run..."
    PER_PKG_OUTPUT=$(go test -coverprofile="${COVERAGE_FILE}" ./...)
    echo "${PER_PKG_OUTPUT}" > "${SUMMARY_FILE}"
fi

# Extract global coverage percentage
GLOBAL_COV_STR=$(go tool cover -func="${COVERAGE_FILE}" | grep total | awk '{print $3}')
GLOBAL_COV=$(echo "${GLOBAL_COV_STR}" | tr -d '%')
echo "==> Global Statement Coverage: ${GLOBAL_COV_STR}"

TABLE_ROWS=""
FAILED_PACKAGES=()

while IFS= read -r line; do
    if [[ "${line}" =~ coverage:\ ([0-9.]+)%\ of\ statements ]]; then
        PKG_NAME=$(echo "${line}" | awk '{print $2}' | sed 's|github.com/umesh0492/go-app-kit/||')
        PKG_COV="${BASH_REMATCH[1]}"
        TABLE_ROWS="${TABLE_ROWS}\n| \`${PKG_NAME}\` | **${PKG_COV}%** |"

        # Check per-package floor
        if (( $(echo "${PKG_COV} < ${PACKAGE_FLOOR}" | bc -l) )); then
            echo "❌ Package ${PKG_NAME} failed floor: ${PKG_COV}% < ${PACKAGE_FLOOR}%"
            FAILED_PACKAGES+=("${PKG_NAME} (${PKG_COV}% < ${PACKAGE_FLOOR}%)")
        fi
    elif [[ "${line}" =~ coverage:\ \[no\ statements\] ]]; then
        PKG_NAME=$(echo "${line}" | awk '{print $2}' | sed 's|github.com/umesh0492/go-app-kit/||')
        TABLE_ROWS="${TABLE_ROWS}\n| \`${PKG_NAME}\` | *No statements (interfaces/types)* |"
    fi
done <<< "${PER_PKG_OUTPUT}"

# Generate Shields.io endpoint badge JSON
COLOR="brightgreen"
if (( $(echo "${GLOBAL_COV} < 90.0" | bc -l) )); then
    COLOR="yellow"
fi
if (( $(echo "${GLOBAL_COV} < 80.0" | bc -l) )); then
    COLOR="red"
fi

mkdir -p "${ROOT_DIR}/.github/badges"
cat << BADGE_EOF > "${ROOT_DIR}/.github/badges/coverage.json"
{
  "schemaVersion": 1,
  "label": "coverage",
  "message": "${GLOBAL_COV_STR}",
  "color": "${COLOR}"
}
BADGE_EOF

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "### 📊 Verified CI/CD Statement Coverage Gate"
        echo ""
        echo "- **Overall Repository Statement Coverage**: \`${GLOBAL_COV_STR}\` (Gate: \`>= ${GLOBAL_FLOOR}%\` ✅)"
        echo ""
        echo "| Package | Statement Coverage |"
        echo "| :--- | :--- |"
        echo -e "${TABLE_ROWS}"
    } >> "${GITHUB_STEP_SUMMARY}"
fi

echo -e "\nDetailed Package Coverage:"
echo -e "${TABLE_ROWS}"

if (( $(echo "${GLOBAL_COV} < ${GLOBAL_FLOOR}" | bc -l) )); then
    echo "❌ Global coverage gate failed: ${GLOBAL_COV}% < ${GLOBAL_FLOOR}%"
    exit 1
fi

if [ ${#FAILED_PACKAGES[@]} -gt 0 ]; then
    echo "❌ One or more packages failed coverage floors:"
    for failed in "${FAILED_PACKAGES[@]}"; do
        echo "   - ${failed}"
    done
    exit 1
fi

echo "✅ All coverage floors met successfully!"
