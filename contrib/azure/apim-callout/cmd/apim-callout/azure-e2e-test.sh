#!/usr/bin/env bash
# Azure APIM Callout End-to-End Test Script
#
# Provisions an Azure environment from scratch, builds and deploys the APIM
# callout service with a custom WAF ruleset, configures APIM gateway policies,
# runs WAF tests against every supported address, and cleans up all resources.
#
# Prerequisites:
#   - az CLI authenticated (az login)
#   - docker CLI available with buildx (for cross-platform builds)
#   - Run from the dd-trace-go repository root
#
# Usage:
#   ./contrib/azure/apim-callout/cmd/apim-callout/azure-e2e-test.sh [--no-cleanup]

set -euo pipefail

# ─── Paths ──────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTRIB_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
REPO_ROOT="$(cd "$CONTRIB_DIR/../../.." && pwd)"

DOCKERFILE="$SCRIPT_DIR/Dockerfile"
FULL_POLICY="$CONTRIB_DIR/deploy/azure/policies/azure-apim-full.xml"
WAF_RULES="$REPO_ROOT/internal/appsec/testdata/user_rules.json"

# ─── Configuration ──────────────────────────────────────────────────────────────

SUFFIX="$(date +%s | tail -c 7)"
RESOURCE_GROUP="dd-apim-e2e-rg-$SUFFIX"
LOCATION="westeurope"
APIM_NAME="ddapime2e$SUFFIX"
ACR_NAME="ddapime2eacr$SUFFIX"
ACI_NAME="dd-apim-callout-e2e"
ACI_DNS_LABEL="ddapimcallout$SUFFIX"
APIM_API_ID="e2e-test-api"
CLEANUP=true
TEMP_DIR=$(mktemp -d)

[[ "${1:-}" == "--no-cleanup" ]] && CLEANUP=false

# ─── Helpers ────────────────────────────────────────────────────────────────────

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
pass_count=0; fail_count=0

log()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()   { echo -e "${GREEN}[PASS]${NC} $*"; pass_count=$((pass_count + 1)); }
fail() { echo -e "${RED}[FAIL]${NC} $*"; fail_count=$((fail_count + 1)); }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
die()  { echo -e "${RED}[FATAL]${NC} $*" >&2; do_cleanup || true; exit 1; }
trap 'die "Script failed at line $LINENO"' ERR

expect_status() {
  local name="$1" expected="$2"; shift 2
  local actual
  actual=$(curl -s -o /dev/null -w "%{http_code}" --max-time 15 "$@" 2>/dev/null) || true
  if [[ "$actual" == "$expected" ]]; then
    ok "$name — HTTP $actual"
  else
    fail "$name — expected HTTP $expected, got HTTP $actual"
  fi
}

expect_body() {
  local name="$1" substr="$2"; shift 2
  local body
  body=$(curl -s --max-time 15 "$@" 2>/dev/null) || true
  if echo "$body" | grep -q "$substr"; then
    ok "$name — body contains '$substr'"
  else
    fail "$name — body missing '$substr'. Got: $(echo "$body" | head -c 200)"
  fi
}

do_cleanup() {
  if [[ "$CLEANUP" == true ]]; then
    log "Cleaning up: deleting resource group $RESOURCE_GROUP"
    az group delete --name "$RESOURCE_GROUP" --yes --no-wait 2>/dev/null || true
  else
    warn "Skipping cleanup (--no-cleanup). Delete manually:"
    warn "  az group delete --name $RESOURCE_GROUP --yes"
  fi
  rm -rf "$TEMP_DIR"
}

# ─── Phase 1: Prerequisites ────────────────────────────────────────────────────

log "Phase 1: Validating prerequisites"
for cmd in az docker curl python3; do
  command -v "$cmd" >/dev/null 2>&1 || die "$cmd not found"
done
az account show >/dev/null 2>&1 || die "Not logged into Azure (run: az login)"
docker info >/dev/null 2>&1     || die "Docker daemon not running"
for f in "$DOCKERFILE" "$FULL_POLICY" "$WAF_RULES"; do
  [[ -f "$f" ]] || die "Required file not found: $f"
done

SUBSCRIPTION_ID=$(az account show --query id -o tsv)
log "Subscription: $(az account show --query name -o tsv) ($SUBSCRIPTION_ID)"
log "Resources: RG=$RESOURCE_GROUP APIM=$APIM_NAME ACR=$ACR_NAME Location=$LOCATION"

# ─── Phase 2: Register Providers ───────────────────────────────────────────────

log "Phase 2: Registering resource providers"
for ns in Microsoft.ApiManagement Microsoft.ContainerRegistry Microsoft.ContainerInstance; do
  state=$(az provider show --namespace "$ns" --query registrationState -o tsv 2>/dev/null || echo "NotRegistered")
  if [[ "$state" != "Registered" ]]; then
    log "  Registering $ns..."
    az provider register --namespace "$ns" --wait >/dev/null 2>&1
  fi
done
log "  All providers registered"

# ─── Phase 3: Create Resource Group ────────────────────────────────────────────

log "Phase 3: Creating resource group"
az group create --name "$RESOURCE_GROUP" --location "$LOCATION" -o none

# ─── Phase 4: Build Docker Image ───────────────────────────────────────────────

log "Phase 4: Building Docker image (linux/amd64) with custom WAF rules"

docker build \
  --platform linux/amd64 \
  --target e2e \
  -t apim-callout-e2e:latest \
  -f "$DOCKERFILE" \
  "$REPO_ROOT" 2>&1 | tail -3

log "  Image built"

# ─── Phase 5: Create ACR & Push ────────────────────────────────────────────────

log "Phase 5: Creating ACR and pushing image"
az acr create \
  --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" --sku Basic --admin-enabled true -o none

az acr login --name "$ACR_NAME" >/dev/null 2>&1
ACR_SERVER="$ACR_NAME.azurecr.io"
docker tag apim-callout-e2e:latest "$ACR_SERVER/apim-callout:latest"
docker push "$ACR_SERVER/apim-callout:latest" 2>&1 | tail -2
log "  Pushed to $ACR_SERVER"

# ─── Phase 6: Create APIM ──────────────────────────────────────────────────────

log "Phase 6: Creating APIM instance (Consumption tier)"
az apim create \
  --name "$APIM_NAME" --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" \
  --publisher-email "e2e-test@datadoghq.com" --publisher-name "DD E2E Test" \
  --sku-name Consumption -o none

APIM_GATEWAY="https://$APIM_NAME.azure-api.net"
log "  Gateway: $APIM_GATEWAY"

# ─── Phase 7: Deploy to ACI ────────────────────────────────────────────────────

log "Phase 7: Deploying callout service to ACI"
ACR_PASSWORD=$(az acr credential show --name "$ACR_NAME" --query "passwords[0].value" -o tsv)

az container create \
  --resource-group "$RESOURCE_GROUP" --name "$ACI_NAME" \
  --image "$ACR_SERVER/apim-callout:latest" \
  --registry-login-server "$ACR_SERVER" \
  --registry-username "$ACR_NAME" --registry-password "$ACR_PASSWORD" \
  --dns-name-label "$ACI_DNS_LABEL" \
  --ports 8080 8081 --os-type Linux --cpu 1 --memory 1 \
  --location "$LOCATION" \
  --environment-variables \
    DD_APPSEC_ENABLED=true \
    DD_APPSEC_RULES=/app/rules.json \
    DD_AGENT_HOST=none \
  -o none

ACI_FQDN=$(az container show \
  --resource-group "$RESOURCE_GROUP" --name "$ACI_NAME" \
  --query ipAddress.fqdn -o tsv)
CALLOUT_URL="http://$ACI_FQDN:8080"
HEALTH_URL="http://$ACI_FQDN:8081"
log "  Callout: $CALLOUT_URL"

# ─── Phase 8: Wait for Health ──────────────────────────────────────────────────

log "Phase 8: Waiting for callout service"
for i in $(seq 1 30); do
  curl -sf "$HEALTH_URL/" >/dev/null 2>&1 && break
  [[ $i -eq 30 ]] && die "Callout service not healthy after 150s"
  sleep 5
done
log "  Service healthy"

# ─── Phase 9: Configure APIM API & Policies ────────────────────────────────────

log "Phase 9: Configuring APIM API and policies"

az apim api create \
  --resource-group "$RESOURCE_GROUP" --service-name "$APIM_NAME" \
  --api-id "$APIM_API_ID" --display-name "E2E Test API" \
  --path /test --service-url "https://httpbin.org" \
  --protocols https --subscription-required false -o none

for method in GET POST PUT DELETE; do
  az apim api operation create \
    --resource-group "$RESOURCE_GROUP" --service-name "$APIM_NAME" \
    --api-id "$APIM_API_ID" \
    --operation-id "$(echo "$method" | tr '[:upper:]' '[:lower:]')-anything" \
    --display-name "$method Anything" --method "$method" \
    --url-template "/anything/*" -o none
done

az apim api operation create \
  --resource-group "$RESOURCE_GROUP" --service-name "$APIM_NAME" \
  --api-id "$APIM_API_ID" \
  --operation-id "get-response-headers" --display-name "GET Response Headers" \
  --method GET --url-template "/response-headers" -o none

# Replace the placeholder in the policy file with the actual ACI callout URL.
# The repo file uses https://<dd-apim-callout-host>:8080 — ACI has no TLS.
sed "s|https://<dd-apim-callout-host>:8080|$CALLOUT_URL|g" "$FULL_POLICY" > "$TEMP_DIR/policy.xml"

# Apply policy via REST API
python3 -c "
import json
with open('$TEMP_DIR/policy.xml') as f:
    xml = f.read()
print(json.dumps({'properties': {'format': 'rawxml', 'value': xml}}))
" > "$TEMP_DIR/policy-body.json"

az rest --method PUT \
  --uri "https://management.azure.com/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$RESOURCE_GROUP/providers/Microsoft.ApiManagement/service/$APIM_NAME/apis/$APIM_API_ID/policies/policy?api-version=2024-05-01" \
  --body @"$TEMP_DIR/policy-body.json" \
  -o none

log "  Policies applied"
log "  Waiting for APIM policy propagation..."
sleep 15

# ─── Phase 10: Tests ───────────────────────────────────────────────────────────

log ""
log "══════════════════════════════════════════════════════════════════"
log " Phase 10: WAF E2E Tests"
log "══════════════════════════════════════════════════════════════════"

BASE="$APIM_GATEWAY/test"

# ── 1. Passthrough ──────────────────────────────────────────────────────────────

log ""
log "── 1. Normal passthrough (no attack) ──"

expect_status "GET /anything/hello" 200 \
  "$BASE/anything/hello"

expect_status "POST /anything/submit (JSON body)" 200 \
  -X POST "$BASE/anything/submit" \
  -H "Content-Type: application/json" \
  -d '{"user":"test","email":"a@b.com"}'

expect_body "GET echoes via httpbin" "httpbin.org" \
  "$BASE/anything/hello"

# ── 2. server.request.headers.no_cookies (User-Agent) ──────────────────────────

log ""
log "── 2. server.request.headers.no_cookies (User-Agent) ──"

expect_status "Block UA (dd-test-scanner-log-block) → 403" 403 \
  -A "dd-test-scanner-log-block" "$BASE/anything/hello"

expect_body "Block body has Datadog message" "You've been blocked" \
  -A "dd-test-scanner-log-block" "$BASE/anything/hello"

expect_status "Monitor UA (dd-test-scanner-log) → 200 (detect only)" 200 \
  -A "dd-test-scanner-log" "$BASE/anything/hello"

# ── 3. server.request.query ────────────────────────────────────────────────────

log ""
log "── 3. server.request.query ──"

expect_status "Query block (match-request-query) → 418" 418 \
  "$BASE/anything/search?match=match-request-query"

expect_status "Query safe value → 200" 200 \
  "$BASE/anything/search?match=safe-value"

# ── 4. server.request.cookies ──────────────────────────────────────────────────

log ""
log "── 4. server.request.cookies ──"

expect_status "Cookie block (jdfoSDGFkivRG_234) → 418" 418 \
  -H "Cookie: foo=jdfoSDGFkivRG_234" "$BASE/anything/hello"

expect_status "Cookie safe value → 200" 200 \
  -H "Cookie: foo=safe-value" "$BASE/anything/hello"

# ── 5. http.client_ip ──────────────────────────────────────────────────────────

log ""
log "── 5. http.client_ip ──"

expect_status "Blocked IP (111.222.111.222) → 403" 403 \
  -H "X-Forwarded-For: 111.222.111.222" "$BASE/anything/hello"

expect_status "Safe IP (1.2.3.4) → 200" 200 \
  -H "X-Forwarded-For: 1.2.3.4" "$BASE/anything/hello"

# ── 6. server.request.body ─────────────────────────────────────────────────────

log ""
log "── 6. server.request.body ──"

expect_status 'Body block (PHP global: $_GET) → 403' 403 \
  -X POST "$BASE/anything/submit" \
  -H "Content-Type: application/json" \
  -d '{"input":"$_GET"}'

expect_status "Body safe value → 200" 200 \
  -X POST "$BASE/anything/submit" \
  -H "Content-Type: application/json" \
  -d '{"input":"safe-value"}'

# ── 7. server.response.headers.no_cookies ──────────────────────────────────────

log ""
log "── 7. server.response.headers.no_cookies ──"

expect_status "Response header block (match-response-header) → 418" 418 \
  "$BASE/response-headers?test=match-response-header"

expect_status "Response header safe value → 200" 200 \
  "$BASE/response-headers?test=safe-value"

# ── 8. Health check ─────────────────────────────────────────────────────────────

log ""
log "── 8. Health check ──"

expect_body "Health check returns ok" '"status":"ok"' "$HEALTH_URL/"

# ─── Logs (collect while container is still running) ────────────────────────────

log ""
log "Container logs (last 20 lines)"
az container logs --resource-group "$RESOURCE_GROUP" --name "$ACI_NAME" \
  | grep -v "CONFIGURATION\|instrumented\|available_version" | tail -20

# ── 9. Fail-open (last — stops the container without restarting) ───────────────

log ""
log "── 9. Fail-open (callout service down) ──"

log "  Stopping callout service..."
az container stop --resource-group "$RESOURCE_GROUP" --name "$ACI_NAME" -o none
sleep 10

expect_status "Request passes when callout is down → 200" 200 \
  "$BASE/anything/hello"

# ─── Results ────────────────────────────────────────────────────────────────────

log ""
log "══════════════════════════════════════════════════════════════════"
log " Results: ${GREEN}$pass_count passed${NC}, ${RED}$fail_count failed${NC}"
log "══════════════════════════════════════════════════════════════════"

# ─── Cleanup ────────────────────────────────────────────────────────────────────

log ""
do_cleanup

if [[ $fail_count -gt 0 ]]; then
  echo ""
  die "$fail_count test(s) failed"
fi

log "All $pass_count tests passed!"
exit 0
