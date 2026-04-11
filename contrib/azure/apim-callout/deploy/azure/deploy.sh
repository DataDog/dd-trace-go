#!/usr/bin/env bash
# Datadog APIM Callout — Deployment Script
# One-command wrapper around az deployment group create with smart defaults.
#
# Usage:
#   ./deploy.sh --resource-group <rg> --apim-name <name> --api-ids "api1,api2" \
#     --log-analytics-workspace-id <id> [--deploy-policy] [--what-if] [--help]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- Defaults ---
RESOURCE_GROUP=""
APIM_NAME=""
APIM_RG=""
TARGET_API_IDS=()
LOG_ANALYTICS_WORKSPACE_ID=""
DD_API_KEY="${DD_API_KEY:-}"
DD_SITE="${DD_SITE:-datadoghq.com}"
DEPLOY_POLICY=""
WHAT_IF=false
NAME_PREFIX="dd-apim"
CONTAINER_IMAGE="ghcr.io/datadog/dd-trace-go/apim-callout:latest"
VNET_ADDRESS_PREFIX="10.0.0.0/16"
EXISTING_VNET_ID=""
EXISTING_ACA_SUBNET_ID=""
EXISTING_ACI_SUBNET_ID=""
ENABLE_HTTPS=false
ENABLE_LOCKS=true
MIN_REPLICAS=1
MAX_REPLICAS=10
CONCURRENT_REQUESTS="20"

usage() {
  cat <<EOF
Datadog APIM Callout — Azure Deployment

USAGE:
  ./deploy.sh [OPTIONS]

REQUIRED:
  --resource-group, -g <name>     Resource group for deployment
  --apim-name <name>              Existing APIM service name

OPTIONAL:
  --log-analytics-workspace-id <id>  Log Analytics workspace resource ID (enables log collection)
  --api-ids <id1,id2,...>         Comma-separated APIM API IDs (default: All APIs)
  --deploy-policy                 Force policy deployment (overrides smart default)
  --no-deploy-policy              Force skip policy deployment
  --what-if                       Run what-if analysis without deploying
  --name-prefix <prefix>          Resource name prefix (default: dd-apim)
  --container-image <image>       Callout container image
  --dd-site <site>                Datadog site (default: datadoghq.com)
  --apim-resource-group <rg>      APIM resource group (if different)
  --vnet-address-prefix <cidr>    VNet address space (default: 10.0.0.0/16)
  --existing-vnet-id <id>         Use existing VNet
  --existing-aca-subnet-id <id>   Use existing ACA subnet
  --existing-aci-subnet-id <id>   Use existing ACI subnet
  --enable-https                  Enforce HTTPS on callout ingress (ACA TLS termination)
  --no-locks                      Disable CanNotDelete resource locks
  --min-replicas <n>              Min replicas (default: 1)
  --max-replicas <n>              Max replicas (default: 10)
  --concurrent-requests <n>       KEDA HTTP threshold (default: 20)
  -h, --help                      Show this help

ENVIRONMENT:
  DD_API_KEY    Datadog API key (or will prompt)
  DD_SITE       Datadog site (default: datadoghq.com)

EXAMPLES:
  # Dry run
  ./deploy.sh -g my-rg --apim-name my-apim --api-ids "echo-api" \\
    --log-analytics-workspace-id "/subscriptions/.../workspaces/my-ws" --what-if

  # Deploy with smart policy default
  ./deploy.sh -g my-rg --apim-name my-apim --api-ids "echo-api,petstore" \\
    --log-analytics-workspace-id "/subscriptions/.../workspaces/my-ws"

  # Force policy deployment
  ./deploy.sh -g my-rg --apim-name my-apim --api-ids "echo-api" \\
    --log-analytics-workspace-id "/subscriptions/.../workspaces/my-ws" --deploy-policy
EOF
  exit 0
}

# --- Parse arguments ---
while [[ $# -gt 0 ]]; do
  case "$1" in
    -g|--resource-group) RESOURCE_GROUP="$2"; shift 2 ;;
    --apim-name) APIM_NAME="$2"; shift 2 ;;
    --api-ids) IFS=',' read -ra TARGET_API_IDS <<< "$2"; shift 2 ;;
    --log-analytics-workspace-id) LOG_ANALYTICS_WORKSPACE_ID="$2"; shift 2 ;;
    --deploy-policy) DEPLOY_POLICY="true"; shift ;;
    --no-deploy-policy) DEPLOY_POLICY="false"; shift ;;
    --what-if) WHAT_IF=true; shift ;;
    --name-prefix) NAME_PREFIX="$2"; shift 2 ;;
    --container-image) CONTAINER_IMAGE="$2"; shift 2 ;;
    --dd-site) DD_SITE="$2"; shift 2 ;;
    --apim-resource-group) APIM_RG="$2"; shift 2 ;;
    --vnet-address-prefix) VNET_ADDRESS_PREFIX="$2"; shift 2 ;;
    --existing-vnet-id) EXISTING_VNET_ID="$2"; shift 2 ;;
    --existing-aca-subnet-id) EXISTING_ACA_SUBNET_ID="$2"; shift 2 ;;
    --existing-aci-subnet-id) EXISTING_ACI_SUBNET_ID="$2"; shift 2 ;;
    --min-replicas) MIN_REPLICAS="$2"; shift 2 ;;
    --max-replicas) MAX_REPLICAS="$2"; shift 2 ;;
    --concurrent-requests) CONCURRENT_REQUESTS="$2"; shift 2 ;;
    --enable-https) ENABLE_HTTPS=true; shift ;;
    --no-locks) ENABLE_LOCKS=false; shift ;;
    -h|--help) usage ;;
    *) echo "ERROR: Unknown option: $1"; usage ;;
  esac
done

# --- Validate required params ---
missing=()
[[ -z "$RESOURCE_GROUP" ]] && missing+=("--resource-group")
[[ -z "$APIM_NAME" ]] && missing+=("--apim-name")
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: Missing required parameters: ${missing[*]}"
  echo "Run with --help for usage."
  exit 1
fi

# Default APIM RG to deployment RG
APIM_RG="${APIM_RG:-$RESOURCE_GROUP}"

# --- Validate prerequisites ---
echo "==> Checking prerequisites..."

# az CLI
if ! command -v az &>/dev/null; then
  echo "ERROR: Azure CLI (az) is not installed. Install from https://aka.ms/install-azure-cli"
  exit 1
fi

# az logged in
if ! az account show &>/dev/null; then
  echo "ERROR: Not logged in to Azure. Run 'az login' first."
  exit 1
fi

# bicep installed
if ! az bicep version &>/dev/null; then
  echo "WARNING: Bicep CLI not found. Installing..."
  az bicep install
fi

# Resource group exists
if ! az group show --name "$RESOURCE_GROUP" &>/dev/null; then
  echo "ERROR: Resource group '$RESOURCE_GROUP' does not exist."
  exit 1
fi

# Validate APIM tier (reject Consumption)
echo "==> Validating APIM service '$APIM_NAME'..."
apim_sku=$(az apim show \
  --resource-group "$APIM_RG" \
  --name "$APIM_NAME" \
  --query "sku.name" -o tsv 2>/dev/null || echo "")

if [[ -z "$apim_sku" ]]; then
  echo "ERROR: APIM service '$APIM_NAME' not found in resource group '$APIM_RG'."
  exit 1
fi

if [[ "$apim_sku" == "Consumption" ]]; then
  echo "ERROR: APIM Consumption tier does not support VNet integration."
  echo "  The Datadog APIM Callout requires Standard v2, Developer, or Premium tier."
  echo "  Current tier: $apim_sku"
  exit 1
fi

echo "  APIM tier: $apim_sku (supported)"

# Check APIM VNet integration
apim_vnet_type=$(az apim show \
  --resource-group "$APIM_RG" \
  --name "$APIM_NAME" \
  --query "virtualNetworkType" -o tsv 2>/dev/null || echo "None")

if [[ "$apim_vnet_type" == "None" || "$apim_vnet_type" == "null" ]]; then
  echo "WARNING: APIM '$APIM_NAME' does not have VNet integration configured."
  echo "  For the callout service to work, APIM needs outbound VNet integration."
  echo "  Configure it in Azure Portal: APIM > Network > Virtual network > External/Internal"
  echo "  Or for Standard v2: APIM > Network > Outbound > VNet integration"
  echo ""
fi

# --- DD_API_KEY ---
if [[ -z "$DD_API_KEY" ]]; then
  if [[ -t 0 ]]; then
    echo -n "Enter Datadog API key: "
    read -rs DD_API_KEY
    echo ""
  fi
  if [[ -z "$DD_API_KEY" ]]; then
    echo "ERROR: DD_API_KEY environment variable is required."
    echo "  Export it before running: export DD_API_KEY=<your-key>"
    exit 1
  fi
fi

# --- Smart default for deployPolicy ---
# Checks the existing APIM policy content to decide whether to deploy.
#   - Already has Datadog callout → skip (already deployed)
#   - Default skeleton only (no real policy elements) → safe to deploy
#   - Has custom content → warn and skip to avoid overwriting
#
# "Real content" means XML elements beyond the default skeleton:
#   <policies>, <inbound>, <outbound>, <backend>, <base>, <forward-request>
has_custom_policy_content() {
  local policy_xml="$1"
  [[ -z "$policy_xml" ]] && return 1

  # Already has Datadog callout?
  if echo "$policy_xml" | grep -q "ddPhase1Response"; then
    echo "already-deployed"
    return 0
  fi

  # Strip XML comments, then remove all default skeleton tags.
  # If anything meaningful remains, there's custom content.
  local stripped
  stripped=$(echo "$policy_xml" \
    | sed '/<!--/,/-->/d' \
    | tr -d '\t\n\r' \
    | sed 's/<[[:space:]]*policies[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*policies[^>]*>//g' \
    | sed 's/<[[:space:]]*inbound[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*inbound[^>]*>//g' \
    | sed 's/<[[:space:]]*outbound[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*outbound[^>]*>//g' \
    | sed 's/<[[:space:]]*backend[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*backend[^>]*>//g' \
    | sed 's/<[[:space:]]*base[^>]*>//g' \
    | sed 's/<[[:space:]]*forward-request[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*forward-request[^>]*>//g' \
    | sed 's/<[[:space:]]*on-error[^>]*>//g' \
    | sed 's/<[[:space:]]*\/[[:space:]]*on-error[^>]*>//g' \
    | tr -d '[:space:]')

  if [[ -z "$stripped" ]]; then
    echo "skeleton-only"
    return 0
  fi

  echo "custom-content"
  return 0
}

check_policy() {
  local label="$1" policy_xml="$2"
  local result
  result=$(has_custom_policy_content "$policy_xml")
  case "$result" in
    already-deployed)
      DEPLOY_POLICY=false
      echo "  $label: Datadog callout already deployed. Skipping."
      ;;
    skeleton-only)
      echo "  $label: Default policy (no custom content). Safe to deploy."
      ;;
    custom-content)
      DEPLOY_POLICY=false
      echo "  WARNING: $label has existing custom policy content."
      echo "  Set --deploy-policy to overwrite, or merge manually using the fragments below."
      ;;
  esac
}

if [[ -z "$DEPLOY_POLICY" ]]; then
  echo "==> Checking existing policies for smart default..."
  SUB_ID=$(az account show --query id -o tsv)
  if [[ ${#TARGET_API_IDS[@]} -eq 0 ]]; then
    existing=$(az rest --method get \
      --url "https://management.azure.com/subscriptions/$SUB_ID/resourceGroups/$APIM_RG/providers/Microsoft.ApiManagement/service/$APIM_NAME/policies/policy?api-version=2023-09-01-preview" \
      --query "properties.value" -o tsv 2>/dev/null || echo "")
    check_policy "All APIs (service-level)" "$existing"
  else
    for api_id in "${TARGET_API_IDS[@]}"; do
      existing=$(az rest --method get \
        --url "https://management.azure.com/subscriptions/$SUB_ID/resourceGroups/$APIM_RG/providers/Microsoft.ApiManagement/service/$APIM_NAME/apis/$api_id/policies/policy?api-version=2023-09-01-preview" \
        --query "properties.value" -o tsv 2>/dev/null || echo "")
      check_policy "API '$api_id'" "$existing"
      [[ "$DEPLOY_POLICY" == "false" ]] && break
    done
  fi
  DEPLOY_POLICY="${DEPLOY_POLICY:-true}"
  echo "  Smart default: deployPolicy=$DEPLOY_POLICY"
fi

# --- Validate API IDs (alphanumeric, dot, dash, underscore only) ---
for api_id in "${TARGET_API_IDS[@]}"; do
  if [[ ! "$api_id" =~ ^[a-zA-Z0-9._-]+$ ]]; then
    echo "ERROR: Invalid API ID '$api_id'. Only alphanumeric, dot, dash, underscore allowed."
    exit 1
  fi
done

# --- Build API IDs JSON array ---
api_ids_json="["
for i in "${!TARGET_API_IDS[@]}"; do
  [[ $i -gt 0 ]] && api_ids_json+=","
  api_ids_json+="\"${TARGET_API_IDS[$i]}\""
done
api_ids_json+="]"

# --- Write secure parameters to temp file (keeps DD_API_KEY out of process list) ---
secure_params_file=$(mktemp)
trap 'rm -f "$secure_params_file"' EXIT
cat > "$secure_params_file" <<JSONEOF
{
  "\$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "datadogApiKey": { "value": "$DD_API_KEY" }
  }
}
JSONEOF
chmod 600 "$secure_params_file"

# --- Construct deployment parameters (non-secret values only) ---
deploy_params=(
  "apimServiceName=$APIM_NAME"
  "apimResourceGroup=$APIM_RG"
  "targetApiIds=$api_ids_json"
  "logAnalyticsWorkspaceId=$LOG_ANALYTICS_WORKSPACE_ID"
  "deployPolicy=$DEPLOY_POLICY"
  "namePrefix=$NAME_PREFIX"
  "containerImage=$CONTAINER_IMAGE"
  "datadogSite=$DD_SITE"
  "vnetAddressPrefix=$VNET_ADDRESS_PREFIX"
  "minReplicas=$MIN_REPLICAS"
  "maxReplicas=$MAX_REPLICAS"
  "concurrentRequestsThreshold=$CONCURRENT_REQUESTS"
  "enableHttps=$ENABLE_HTTPS"
  "enableLocks=$ENABLE_LOCKS"
)

# Add optional existing resource IDs
[[ -n "$EXISTING_VNET_ID" ]] && deploy_params+=("existingVnetId=$EXISTING_VNET_ID")
[[ -n "$EXISTING_ACA_SUBNET_ID" ]] && deploy_params+=("existingAcaSubnetId=$EXISTING_ACA_SUBNET_ID")
[[ -n "$EXISTING_ACI_SUBNET_ID" ]] && deploy_params+=("existingAciSubnetId=$EXISTING_ACI_SUBNET_ID")

# Build -p flags
param_flags=()
for p in "${deploy_params[@]}"; do
  param_flags+=("-p" "$p")
done

# --- What-if or Deploy ---
if [[ "$WHAT_IF" == true ]]; then
  echo ""
  echo "==> Running what-if analysis..."
  az deployment group what-if \
    --resource-group "$RESOURCE_GROUP" \
    --template-file "$SCRIPT_DIR/main.bicep" \
    --parameters "@$secure_params_file" \
    "${param_flags[@]}"
  echo ""
  echo "==> What-if complete. No resources were modified."
  echo "  Remove --what-if to deploy."
else
  echo ""
  echo "==> Running what-if analysis first..."
  az deployment group what-if \
    --resource-group "$RESOURCE_GROUP" \
    --template-file "$SCRIPT_DIR/main.bicep" \
    --parameters "@$secure_params_file" \
    "${param_flags[@]}"

  echo ""
  echo -n "Proceed with deployment? [y/N] "
  read -r confirm
  if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "Deployment cancelled."
    exit 0
  fi

  echo ""
  echo "==> Deploying..."
  result=$(az deployment group create \
    --resource-group "$RESOURCE_GROUP" \
    --template-file "$SCRIPT_DIR/main.bicep" \
    --parameters "@$secure_params_file" \
    "${param_flags[@]}" \
    --query "properties.outputs" -o json)

  echo ""
  echo "============================================"
  echo "  Datadog APIM Callout — Deployment Summary"
  echo "============================================"

  callout_url=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('calloutBaseUrl',{}).get('value','N/A'))" 2>/dev/null || echo "N/A")
  aca_fqdn=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('acaFqdn',{}).get('value','N/A'))" 2>/dev/null || echo "N/A")
  agent_ip=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('agentIp',{}).get('value','N/A'))" 2>/dev/null || echo "N/A")
  echo ""
  echo "  Callout URL:  $callout_url"
  echo "  ACA FQDN:     $aca_fqdn"
  echo "  Agent IP:     $agent_ip"
  echo "  Policy deployed: $DEPLOY_POLICY"
  echo ""

  if [[ "$DEPLOY_POLICY" == "false" ]]; then
    echo "  Policy was NOT deployed to APIM. To apply manually:"
    echo ""
    echo "  Option 1: Re-run with --deploy-policy"
    echo ""
    echo "  Option 2: Patch your existing policy XML:"
    echo "    sed -i 's|https://<dd-apim-callout-host>:8080|${callout_url}|g' your-policy.xml"
    echo ""
    echo "  Option 3: Merge the policy fragments from azure-apim-full.xml"
    echo "    See: contrib/azure/apim-callout/deploy/azure/policies/azure-apim-full.xml"
    echo ""
  fi

  echo "  Rollback:"
  echo "    - Policy: re-deploy with --no-deploy-policy or revert in Azure Portal"
  echo "    - Full: az group delete --name $RESOURCE_GROUP"
  echo ""
fi
