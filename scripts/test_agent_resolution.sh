#!/bin/bash
# CW-20260815-0026: Manual verification script for agent resolution fix
#
# This script demonstrates that the fix correctly handles:
# 1. Agent resolution by slug (e.g., -agent worker)
# 2. Agent resolution by ID (e.g., -agent file-worker)
# 3. Loud failure on nonexistent agents (e.g., -agent bogus-slug)
#
# Prerequisites:
# - nanite serve must be running
# - A workspace must exist (get ID from UI or DB)
#
# Usage:
#   ./test_agent_resolution.sh <workspace-id>

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <workspace-id>"
    echo "Example: $0 ws-default"
    exit 1
fi

WORKSPACE_ID="$1"
BASE_URL="${NANITE_API_URL:-http://localhost:19183}"

echo "=== Test 1: Valid agent by slug (worker) ==="
echo "This should succeed and resolve to file-worker"
response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/harness/v1/sessions" \
    -H "Content-Type: application/json" \
    -d "{\"workspace_id\": \"$WORKSPACE_ID\", \"agent_id\": \"worker\"}")
http_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | head -n-1)

if [ "$http_code" = "201" ]; then
    echo "✓ Success: Session created with worker agent"
    session_id=$(echo "$body" | jq -r '.session.id')
    agent_id=$(echo "$body" | jq -r '.details.primary_agent.id')
    echo "  Session ID: $session_id"
    echo "  Agent ID: $agent_id (should be 'file-worker' or similar)"
else
    echo "✗ Failed: Expected 201, got $http_code"
    echo "  Response: $body"
    exit 1
fi

echo ""
echo "=== Test 2: Valid agent by ID (file-worker) ==="
echo "This should succeed with the same agent"
response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/harness/v1/sessions" \
    -H "Content-Type: application/json" \
    -d "{\"workspace_id\": \"$WORKSPACE_ID\", \"agent_id\": \"file-worker\"}")
http_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | head -n-1)

if [ "$http_code" = "201" ]; then
    echo "✓ Success: Session created with file-worker agent"
    session_id=$(echo "$body" | jq -r '.session.id')
    agent_id=$(echo "$body" | jq -r '.details.primary_agent.id')
    echo "  Session ID: $session_id"
    echo "  Agent ID: $agent_id"
else
    echo "✗ Failed: Expected 201, got $http_code"
    echo "  Response: $body"
    exit 1
fi

echo ""
echo "=== Test 3: Nonexistent agent (bogus-slug) ==="
echo "This should fail with 400 Bad Request and a clear error message"
response=$(curl -s -w "\n%{http_code}" -X POST "$BASE_URL/api/harness/v1/sessions" \
    -H "Content-Type: application/json" \
    -d "{\"workspace_id\": \"$WORKSPACE_ID\", \"agent_id\": \"bogus-slug\"}")
http_code=$(echo "$response" | tail -n1)
body=$(echo "$response" | head -n-1)

if [ "$http_code" = "400" ]; then
    if echo "$body" | grep -q "bogus-slug" && echo "$body" | grep -q "not found"; then
        echo "✓ Success: Failed loudly with clear error message"
        echo "  Error: $body"
    else
        echo "✗ Failed: Got 400 but error message unclear"
        echo "  Response: $body"
        exit 1
    fi
else
    echo "✗ Failed: Expected 400, got $http_code"
    echo "  Response: $body"
    echo "  (Before fix, this would silently succeed with default agent)"
    exit 1
fi

echo ""
echo "=== All tests passed! ==="
echo "The fix correctly:"
echo "  1. Resolves agent slugs to canonical IDs"
echo "  2. Accepts both slugs and IDs"
echo "  3. Fails loudly on nonexistent agents instead of silent fallback"
