#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# CHANGELOG Symbol Inventory Verification (go-app-kit)
# Verifies that every package and exported symbol/function/struct named in
# CHANGELOG.md under '### Added' actually exists in the go-app-kit codebase.
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

CHANGELOG_FILE="CHANGELOG.md"
if [ ! -f "$CHANGELOG_FILE" ]; then
    echo "❌ Error: $CHANGELOG_FILE not found."
    exit 1
fi

echo "🔍 Auditing CHANGELOG.md exported symbols against codebase..."

# Extract bullets strictly under '### Added' up to the next heading
ADDED_BULLETS=$(awk '/^### Added/{flag=1; next} /^###|^##/{flag=0} flag && /^[-*]/' "$CHANGELOG_FILE")

if [ -z "$ADDED_BULLETS" ]; then
    echo "⚠️  No bullets found under ### Added in $CHANGELOG_FILE"
    exit 0
fi

FAILED=0

# Whitelist of known non-symbol technical terms/acronyms appearing in bullets
WHITELIST="PostgreSQL|RFC|MIME|HMAC|SHA256|DDL|CSV|JSON|SQL|GST|GSTIN|PAN|Aadhaar|IFSC|INR|BOM|UTF8|API|UUID|CPU|RAM|HTTP|SMTP"

while IFS= read -r bullet; do
    [ -z "$bullet" ] && continue
    echo "  -> Auditing bullet: $bullet"

    # Extract the package/prefix before ':' if present
    PREFIX=""
    if [[ "$bullet" =~ ^[[:space:]]*[-*][[:space:]]*([^:]+): ]]; then
        PREFIX="${BASH_REMATCH[1]}"
        PREFIX=$(echo "$PREFIX" | tr -d '`' | tr -d '[:space:]')
    fi
    
    # Verify package directory exists and contains Go files
    if [ -n "$PREFIX" ]; then
        if [ ! -d "$PREFIX" ] || ! ls "$PREFIX"/*.go >/dev/null 2>&1; then
            echo "     ❌ Error: Package '$PREFIX' does not exist in repository or has no Go files"
            FAILED=1
        fi
    fi

    # 1. Check any backticked token: `SymbolName`
    # Extract tokens enclosed in backticks
    BACKTICK_TOKENS=$(echo "$bullet" | grep -oE '`[^`]+`' | tr -d '`' || true)
    for token in $BACKTICK_TOKENS; do
        [ -z "$token" ] && continue
        # Strip trailing parentheses or commas: e.g. "Func()" -> "Func"
        CLEAN_TOKEN=$(echo "$token" | sed -E 's/\(\)//g' | tr -d ',')
        
        # Skip if it's the package name itself
        if [ "$CLEAN_TOKEN" = "$PREFIX" ] || [ -d "$CLEAN_TOKEN" ]; then
            continue
        fi

        # Skip multi-word phrases (e.g. `SELECT FOR UPDATE SKIP LOCKED`)
        if [[ "$CLEAN_TOKEN" =~ [[:space:]] ]]; then
            continue
        fi

        # Skip version tokens (e.g. v0.1.0)
        if [[ "$CLEAN_TOKEN" =~ ^v[0-9]+ ]]; then
            continue
        fi

        # Check if symbol exists in Go code
        FOUND=0
        if [ -n "$PREFIX" ] && [ -d "$PREFIX" ]; then
            if git grep -qE "\b${CLEAN_TOKEN}\b" -- "$PREFIX/*.go" 2>/dev/null; then
                FOUND=1
            fi
        fi
        if [ "$FOUND" -eq 0 ]; then
            if git grep -qE "\b${CLEAN_TOKEN}\b" -- "*.go" 2>/dev/null; then
                FOUND=1
            fi
        fi

        if [ "$FOUND" -eq 1 ]; then
            echo "     ✅ Verified backticked symbol: '$CLEAN_TOKEN'"
        else
            echo "     ❌ Error: Exported symbol '$CLEAN_TOKEN' not found in codebase!"
            FAILED=1
        fi
    done

    # 2. Check unbackticked CamelCase words like RenderWithChromiumEngine
    # Words with at least two uppercase letters and mixed case
    CAMEL_TOKENS=$(echo "$bullet" | grep -oE '\b[A-Z][a-z0-9]+[A-Z][a-zA-Z0-9]*\b' || true)
    for token in $CAMEL_TOKENS; do
        [ -z "$token" ] && continue
        # Skip whitelisted technical acronyms/words
        if echo "$token" | grep -qE "^($WHITELIST)$"; then
            continue
        fi
        
        # Check if symbol exists in codebase
        FOUND=0
        if [ -n "$PREFIX" ] && [ -d "$PREFIX" ]; then
            if git grep -qE "\b${token}\b" -- "$PREFIX/*.go" 2>/dev/null; then
                FOUND=1
            fi
        fi
        if [ "$FOUND" -eq 0 ]; then
            if git grep -qE "\b${token}\b" -- "*.go" 2>/dev/null; then
                FOUND=1
            fi
        fi

        if [ "$FOUND" -eq 1 ]; then
            echo "     ✅ Verified CamelCase symbol: '$token'"
        else
            echo "     ❌ Error: Exported symbol '$token' not found in codebase!"
            FAILED=1
        fi
    done

done <<< "$ADDED_BULLETS"

if [ "$FAILED" -ne 0 ]; then
    echo "❌ CHANGELOG symbol verification failed. One or more symbols/packages do not exist."
    exit 1
fi

echo "✅ All symbols in CHANGELOG ### Added successfully verified against codebase!"
exit 0
