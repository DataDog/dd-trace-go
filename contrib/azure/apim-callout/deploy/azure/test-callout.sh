#!/usr/bin/env bash
# WAF test script for the APIM callout deployment.
# Sends the same requests as the e2e test script and checks for blocking/passthrough.
#
# NOTE: Blocking tests (expect 403/418) require the callout image to be built
# with --target e2e (includes custom WAF rules). The production image will pass
# all requests through (200) since it has no custom rules loaded.
#
# Usage:
#   ./test-callout.sh [--apim-name NAME] [--resource-group RG] [--api-path PATH]

set -euo pipefail

APIM_NAME="${APIM_NAME:-dd-apim-callout-test}"
APIM_RG="${APIM_RG:-dd-apim-callout-test}"
ACA_NAME="${ACA_NAME:-dd-apim-callout}"
ACA_RG="${ACA_RG:-dd-apim-callout-test}"
API_PATH="/echo"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apim-name) APIM_NAME="$2"; shift 2 ;;
    --resource-group|-g) APIM_RG="$2"; ACA_RG="$2"; shift 2 ;;
    --api-path) API_PATH="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: $0 [--apim-name NAME] [--resource-group RG] [--api-path PATH]"
      echo ""
      echo "Options:"
      echo "  --apim-name    APIM service name (default: dd-apim-callout-test)"
      echo "  -g, --resource-group  Resource group (default: dd-apim-callout-test)"
      echo "  --api-path     API base path (default: /echo)"
      exit 0 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# --- Setup ---

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
pass_count=0; fail_count=0

SUBSCRIPTION_ID=$(az account show --query id -o tsv)
GATEWAY="https://$APIM_NAME.azure-api.net"

echo -e "${BLUE}==> Fetching APIM subscription key...${NC}"
APIM_KEY=$(az rest --method post \
  --url "https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$APIM_RG/providers/Microsoft.ApiManagement/service/$APIM_NAME/subscriptions/master/listSecrets?api-version=2023-09-01-preview" \
  --query 'primaryKey' -o tsv)

BASE="$GATEWAY$API_PATH"
echo -e "${BLUE}==> Gateway: $GATEWAY${NC}"
echo -e "${BLUE}==> Base URL: $BASE${NC}"
echo ""

# --- Helpers ---

expect_status() {
  local name="$1" expected="$2"; shift 2
  local result actual total connect ttfb
  result=$(curl -s -o /dev/null -w '%{http_code} %{time_total} %{time_connect} %{time_starttransfer}' \
    --max-time 15 "$@" -H "Ocp-Apim-Subscription-Key: $APIM_KEY" 2>/dev/null) || true
  actual=$(echo "$result" | awk '{print $1}')
  total=$(echo "$result" | awk '{printf "%.3fs", $2}')
  connect=$(echo "$result" | awk '{printf "%.3fs", $3}')
  ttfb=$(echo "$result" | awk '{printf "%.3fs", $4}')

  if [[ "$actual" == "$expected" ]]; then
    echo -e "  ${GREEN}[PASS]${NC} $name — HTTP $actual  (total=$total connect=$connect ttfb=$ttfb)"
    pass_count=$((pass_count + 1))
  else
    echo -e "  ${RED}[FAIL]${NC} $name — expected HTTP $expected, got HTTP $actual  (total=$total connect=$connect ttfb=$ttfb)"
    fail_count=$((fail_count + 1))
  fi
}

expect_body() {
  local name="$1" substr="$2"; shift 2
  local body
  body=$(curl -s --max-time 15 "$@" -H "Ocp-Apim-Subscription-Key: $APIM_KEY" 2>/dev/null) || true
  if echo "$body" | grep -q "$substr"; then
    echo -e "  ${GREEN}[PASS]${NC} $name — body contains '$substr'"
    pass_count=$((pass_count + 1))
  else
    echo -e "  ${RED}[FAIL]${NC} $name — body missing '$substr'. Got: $(echo "$body" | head -c 200)"
    fail_count=$((fail_count + 1))
  fi
}

# --- 1. Normal passthrough ---

echo -e "${BLUE}── 1. Normal passthrough (no attack) ──${NC}"

expect_status "GET /resource" 200 \
  "$BASE/resource?param1=hello"

expect_status "POST /resource (JSON body)" 200 \
  -X POST "$BASE/resource" \
  -H "Content-Type: application/json" \
  -d '{"user":"test","email":"a@b.com"}'

# --- 2. server.request.headers.no_cookies (User-Agent) ---

echo ""
echo -e "${BLUE}── 2. server.request.headers.no_cookies (User-Agent) ──${NC}"

expect_status "Block UA (dd-test-scanner-log-block) → 403" 403 \
  -A "dd-test-scanner-log-block" "$BASE/resource?param1=hello"

expect_body "Block body has Datadog message" "You've been blocked" \
  -A "dd-test-scanner-log-block" "$BASE/resource?param1=hello"

expect_status "Monitor UA (dd-test-scanner-log) → 200 (detect only)" 200 \
  -A "dd-test-scanner-log" "$BASE/resource?param1=hello"

# --- 3. server.request.query ---

echo ""
echo -e "${BLUE}── 3. server.request.query ──${NC}"

expect_status "Query safe value → 200" 200 \
  "$BASE/resource?match=safe-value"

# --- 4. server.request.cookies ---

echo ""
echo -e "${BLUE}── 4. server.request.cookies ──${NC}"

expect_status "Cookie safe value → 200" 200 \
  -H "Cookie: foo=safe-value" "$BASE/resource?param1=hello"

# --- 5. http.client_ip ---

echo ""
echo -e "${BLUE}── 5. http.client_ip ──${NC}"

expect_status "Safe IP (1.2.3.4) → 200" 200 \
  -H "X-Forwarded-For: 1.2.3.4" "$BASE/resource?param1=hello"

# --- 6. server.request.body ---

echo ""
echo -e "${BLUE}── 6. server.request.body ──${NC}"

expect_status "Body safe value → 200" 200 \
  -X POST "$BASE/resource" \
  -H "Content-Type: application/json" \
  -d '{"input":"safe-value"}'

# --- 7. server.response.headers.no_cookies ---

echo ""
echo -e "${BLUE}── 7. server.response.headers.no_cookies ──${NC}"

expect_status "Response header safe value → 200" 200 \
  "$BASE/resource?test=safe-value"

# --- Results ---

echo ""
echo -e "${BLUE}══════════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE} Results: ${GREEN}$pass_count passed${NC}, ${RED}$fail_count failed${NC}"
echo -e "${BLUE}══════════════════════════════════════════════════════════════════${NC}"

# --- Callout logs ---

echo ""
echo -e "${BLUE}==> Waiting 65s for callout tracer stats...${NC}"
sleep 65

echo -e "${BLUE}==> Callout logs (last 5 lines):${NC}"
az containerapp logs show \
  --resource-group "$ACA_RG" \
  --name "$ACA_NAME" \
  --type console 2>&1 | tail -5 | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        obj = json.loads(line)
        print(f\"  {obj.get('TimeStamp', '?')[:19]}  {obj.get('Log', '?')}\")
    except:
        print(f\"  {line}\")
"

echo ""
if [[ $fail_count -gt 0 ]]; then
  echo -e "${RED}$fail_count test(s) failed.${NC}"
  exit 1
fi

echo -e "${GREEN}All $pass_count tests passed!${NC}"
