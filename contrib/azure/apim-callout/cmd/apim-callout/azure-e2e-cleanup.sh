#!/usr/bin/env bash
# Cleanup script for Azure APIM Callout E2E test resources.
#
# Deletes all resource groups matching the e2e test naming pattern,
# plus any leftover manual test resources.
#
# Usage:
#   ./contrib/azure/apim-callout/cmd/apim-callout/azure-e2e-cleanup.sh

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; BLUE='\033[0;34m'; NC='\033[0m'
log() { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()  { echo -e "${GREEN}[ OK ]${NC} $*"; }
err() { echo -e "${RED}[ERR ]${NC} $*"; }

log "Listing resource groups..."
RG_LIST=$(az group list --query "[?starts_with(name, 'dd-apim-')].name" -o tsv)

if [[ -z "$RG_LIST" ]]; then
  ok "No APIM test resource groups found. Nothing to clean up."
  exit 0
fi

for rg in $RG_LIST; do
  log "Deleting resource group: $rg"
  if az group delete --name "$rg" --yes --no-wait; then
    ok "Scheduled deletion of $rg"
  else
    err "Failed to delete $rg"
  fi
done

# Clean up local docker images from e2e tests
log "Cleaning up local Docker images..."
for img in apim-callout-e2e ddapimcallout; do
  if docker image inspect "$img:latest" >/dev/null 2>&1; then
    docker rmi "$img:latest" 2>/dev/null && ok "Removed $img:latest" || true
  fi
done

# Clean up ACR-tagged images
for img in $(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep 'azurecr.io/apim-callout'); do
  docker rmi "$img" 2>/dev/null && ok "Removed $img" || true
done

ok "Cleanup complete. Resource group deletions may take a few minutes to finalize."
