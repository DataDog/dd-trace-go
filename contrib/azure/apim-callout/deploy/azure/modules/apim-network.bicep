// APIM Network module: configures VNet integration on an existing APIM service.
// Uses a deployment script to run az apim update (PATCH), avoiding the need to
// redeclare all APIM properties in a Bicep PUT.

@description('Azure region')
param location string

@description('Name of the existing APIM service')
param apimServiceName string

@description('Resource group of the APIM service')
param apimResourceGroup string

@description('Resource ID of the APIM subnet (must have an NSG attached)')
param apimSubnetId string

@description('Custom tags to merge with default tags')
param customTags object = {}

var tags = union({ 'dd-component': 'apim-callout' }, customTags)

// Managed identity for the deployment script to call az apim update.
resource scriptIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: 'apim-network-script-id'
  location: location
  tags: tags
}

resource apim 'Microsoft.ApiManagement/service@2023-09-01-preview' existing = {
  name: apimServiceName
}

// Grant API Management Service Contributor on the APIM resource so the script can update APIM.
resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(scriptIdentity.id, apim.id, 'APIMContributor')
  scope: apim
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '312a565d-c81f-4fd8-895a-4e21e48d571c') // API Management Service Contributor
    principalId: scriptIdentity.properties.principalId
    principalType: 'ServicePrincipal'
  }
}

resource configureVnet 'Microsoft.Resources/deploymentScripts@2023-08-01' = {
  name: 'configure-apim-vnet'
  location: location
  tags: tags
  kind: 'AzureCLI'
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${scriptIdentity.id}': {}
    }
  }
  dependsOn: [roleAssignment]
  properties: {
    azCliVersion: '2.63.0'
    retentionInterval: 'PT1H'
    timeout: 'PT45M'
    scriptContent: '''
      #!/bin/bash
      set -euo pipefail

      VNET_TYPE=$(az apim show \
        --resource-group "$APIM_RG" \
        --name "$APIM_NAME" \
        --query "virtualNetworkType" -o tsv)

      if [[ "$VNET_TYPE" == "External" || "$VNET_TYPE" == "Internal" ]]; then
        echo "APIM already has VNet integration ($VNET_TYPE). Skipping."
        echo "{\"vnetType\": \"$VNET_TYPE\", \"changed\": false}" > $AZ_SCRIPTS_OUTPUT_PATH
        exit 0
      fi

      echo "Configuring APIM VNet integration (External mode)..."
      az apim update \
        --resource-group "$APIM_RG" \
        --name "$APIM_NAME" \
        --virtual-network External \
        --set "virtualNetworkConfiguration.subnetResourceId=$APIM_SUBNET_ID" \
        -o none

      echo "{\"vnetType\": \"External\", \"changed\": true}" > $AZ_SCRIPTS_OUTPUT_PATH
    '''
    environmentVariables: [
      { name: 'APIM_RG', value: apimResourceGroup }
      { name: 'APIM_NAME', value: apimServiceName }
      { name: 'APIM_SUBNET_ID', value: apimSubnetId }
    ]
  }
}
