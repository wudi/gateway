#!/bin/bash

# Test script for the API Gateway
# Run this after starting the gateway

set -e

BASE_URL="${GATEWAY_URL:-http://localhost:8080}"
ADMIN_URL="${ADMIN_URL:-http://localhost:8081}"
API_KEY="${API_KEY:-abc123}"
JWT_SECRET="${JWT_SECRET:-your-secret-key-here}"

echo "Testing Gateway at $BASE_URL"
echo "Admin URL: $ADMIN_URL"
echo ""

# Function to print colored output
print_result() {
    if [ $1 -eq 0 ]; then
        echo -e "\033[32m✓ $2\033[0m"
    else
        echo -e "\033[31m✗ $2\033[0m"
    fi
}

# Test health endpoint
echo "=== Health Checks ==="
HEALTH_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$ADMIN_URL/health")
if [ "$HEALTH_RESPONSE" = "200" ]; then
    print_result 0 "Health endpoint returns 200"
else
    print_result 1 "Health endpoint returned $HEALTH_RESPONSE"
fi

READY_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$ADMIN_URL/ready")
if [ "$READY_RESPONSE" = "200" ]; then
    print_result 0 "Ready endpoint returns 200"
else
    print_result 1 "Ready endpoint returned $READY_RESPONSE (may be expected if no backends)"
fi

echo ""

# Test routes endpoint
echo "=== Admin Endpoints ==="
ROUTES=$(curl -s "$ADMIN_URL/routes")
echo "Routes: $ROUTES"

STATS=$(curl -s "$ADMIN_URL/stats")
echo "Stats: $STATS"
echo ""

# Test public endpoint (no auth required)
echo "=== Public Endpoint Test ==="
PUBLIC_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/public/test")
echo "Public endpoint response: $PUBLIC_RESPONSE"
echo ""

# Test protected endpoint without auth
echo "=== Authentication Tests ==="
NOAUTH_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/api/v1/users")
if [ "$NOAUTH_RESPONSE" = "401" ]; then
    print_result 0 "Protected endpoint returns 401 without auth"
else
    print_result 1 "Protected endpoint returned $NOAUTH_RESPONSE without auth (expected 401)"
fi

# Test with API key
APIKEY_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/users")
echo "API key auth response: $APIKEY_RESPONSE"

# Test with invalid API key
INVALID_APIKEY_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" -H "X-API-Key: invalid" "$BASE_URL/api/v1/users")
if [ "$INVALID_APIKEY_RESPONSE" = "401" ]; then
    print_result 0 "Invalid API key returns 401"
else
    print_result 1 "Invalid API key returned $INVALID_APIKEY_RESPONSE (expected 401)"
fi

echo ""

# Test 404
echo "=== Not Found Test ==="
NOTFOUND_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/nonexistent/path")
if [ "$NOTFOUND_RESPONSE" = "404" ]; then
    print_result 0 "Non-existent path returns 404"
else
    print_result 1 "Non-existent path returned $NOTFOUND_RESPONSE (expected 404)"
fi

echo ""

# Test request ID header
echo "=== Headers Test ==="
REQUEST_ID=$(curl -s -I -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/users" 2>/dev/null | grep -i "x-request-id" || echo "")
if [ -n "$REQUEST_ID" ]; then
    print_result 0 "X-Request-ID header present: $REQUEST_ID"
else
    print_result 1 "X-Request-ID header not found"
fi

echo ""
echo "=== Test Complete ==="
