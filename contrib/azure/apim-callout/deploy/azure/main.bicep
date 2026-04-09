// Datadog APIM Callout — Bicep Orchestrator
// Deploys: VNet + ACI (DD Agent) + ACA (callout service) + optional APIM policy
//
// Usage:
//   az deployment group create -g <rg> -f main.bicep -p @parameters/default.bicepparam \
//     datadogApiKey=<key> apimServiceName=<name> targetApiIds='["api1"]' \
//     logAnalyticsWorkspaceId=<id>

metadata description = 'Datadog APIM Callout — deploy VNet + ACI Agent + ACA callout + APIM policy. Do not edit azuredeploy.json manually; regenerate with: az bicep build -f main.bicep --outfile azuredeploy.json'

targetScope = 'resourceGroup'

// --- Required parameters ---

@description('Datadog API key (never logged or output)')
@secure()
param datadogApiKey string

@description('Name of the existing APIM service')
param apimServiceName string

@description('Resource group of the APIM service (defaults to this RG)')
param apimResourceGroup string = resourceGroup().name

@description('Array of APIM API IDs to apply the policy to (empty = All APIs)')
param targetApiIds array = []

@description('Resource ID of an existing Log Analytics workspace (optional, enables ACA log collection and diagnostic settings)')
param logAnalyticsWorkspaceId string = ''

// --- Optional parameters ---

@description('Resource name prefix for all resources')
param namePrefix string = 'dd-apim'

@description('Azure region (defaults to resource group location)')
param location string = resourceGroup().location

@description('Datadog site')
@allowed([
  'datadoghq.com'
  'us3.datadoghq.com'
  'us5.datadoghq.com'
  'datadoghq.eu'
  'ap1.datadoghq.com'
  'ap2.datadoghq.com'
  'ddog-gov.com'
  'datad0g.com' // Datadog staging (internal use only)
])
param datadogSite string = 'datadoghq.com'

@description('Whether to deploy the Datadog policy to APIM APIs')
param deployPolicy bool = false

@description('Enforce HTTPS on the callout ingress (ACA terminates TLS automatically)')
param enableHttps bool = false

@description('Container image for the callout service')
param containerImage string = 'ghcr.io/datadog/dd-trace-go/apim-callout:latest'

@description('VNet address space')
param vnetAddressPrefix string = '10.0.0.0/16'

@description('Resource ID of an existing VNet (empty = create new)')
param existingVnetId string = ''

@description('Resource ID of an existing ACA subnet (empty = create new)')
param existingAcaSubnetId string = ''

@description('Resource ID of an existing ACI subnet (empty = create new)')
param existingAciSubnetId string = ''

@description('Enable CanNotDelete locks on critical resources (disable for dev/test)')
param enableLocks bool = true

@description('Minimum replicas for the callout container app')
@minValue(0)
@maxValue(30)
param minReplicas int = 1

@description('Maximum replicas for the callout container app')
@minValue(1)
@maxValue(300)
param maxReplicas int = 10

@description('KEDA HTTP scaler concurrent requests threshold')
param concurrentRequestsThreshold string = '20'

@description('Custom tags to apply to all resources (merged with default dd-component tag)')
param customTags object = {}

// --- Policy XML ---
// loadTextContent resolves relative to the file it appears in.
var policyXml = loadTextContent('policies/azure-apim-full.xml')

// --- Module: Networking (always) ---
module networking './modules/networking.bicep' = {
  name: '${namePrefix}-networking'
  params: {
    location: location
    namePrefix: namePrefix
    vnetAddressPrefix: vnetAddressPrefix
    existingVnetId: existingVnetId
    existingAcaSubnetId: existingAcaSubnetId
    existingAciSubnetId: existingAciSubnetId
    customTags: customTags
  }
}

// --- Module: DD Agent on ACI (always) ---
module agent './modules/agent.bicep' = {
  name: '${namePrefix}-agent'
  params: {
    location: location
    namePrefix: namePrefix
    datadogApiKey: datadogApiKey
    datadogSite: datadogSite
    aciSubnetId: networking.outputs.aciSubnetId
    enableLocks: enableLocks
    customTags: customTags
  }
}

// --- Module: Container App (always) ---
module containerApp './modules/container-app.bicep' = {
  name: '${namePrefix}-container-app'
  params: {
    location: location
    namePrefix: namePrefix
    containerImage: containerImage
    agentHost: agent.outputs.agentIp
    minReplicas: minReplicas
    maxReplicas: maxReplicas
    concurrentRequestsThreshold: concurrentRequestsThreshold
    acaSubnetId: networking.outputs.acaSubnetId
    logAnalyticsWorkspaceId: logAnalyticsWorkspaceId
    enableHttps: enableHttps
    enableLocks: enableLocks
    customTags: customTags
  }
}

// --- Module: Private DNS (resolves internal ACA FQDN within the VNet) ---
module dns './modules/dns.bicep' = {
  name: '${namePrefix}-dns'
  params: {
    namePrefix: namePrefix
    acaDomain: containerApp.outputs.acaDefaultDomain
    acaStaticIp: containerApp.outputs.acaStaticIp
    vnetId: networking.outputs.vnetId
    customTags: customTags
  }
}

// --- Module: APIM Network (configures VNet integration on existing APIM) ---
// Scoped to the APIM resource group to support cross-RG deployments.
module apimNetwork './modules/apim-network.bicep' = {
  name: '${namePrefix}-apim-network'
  scope: resourceGroup(apimResourceGroup)
  params: {
    apimServiceName: apimServiceName
    apimResourceGroup: apimResourceGroup
    location: location
    apimSubnetId: networking.outputs.apimSubnetId
    customTags: customTags
  }
}

// --- Module: APIM Policy (always invoked for outputs; resource conditional inside) ---
// Scoped to the APIM resource group to support cross-RG deployments.
// Depends on apimNetwork to ensure VNet is configured before policy injection.
module apimPolicy './modules/apim-policy.bicep' = {
  name: '${namePrefix}-apim-policy'
  scope: resourceGroup(apimResourceGroup)
  params: {
    deployPolicy: deployPolicy
    apimServiceName: apimServiceName
    targetApiIds: targetApiIds
    calloutBaseUrl: containerApp.outputs.calloutBaseUrl
    policyXml: policyXml
  }
}

// --- Outputs ---
output acaFqdn string = containerApp.outputs.acaFqdn
output calloutBaseUrl string = containerApp.outputs.calloutBaseUrl
output agentIp string = agent.outputs.agentIp
output vnetId string = networking.outputs.vnetId
output apimSubnetId string = networking.outputs.apimSubnetId
output policyPatchCommand string = apimPolicy.outputs.policyPatchCommand
output policyFragments object = apimPolicy.outputs.policyFragments
output appliedApiIds array = apimPolicy.outputs.appliedApiIds
output deploymentSummary string = 'Callout: ${containerApp.outputs.calloutBaseUrl} | Agent: ${agent.outputs.agentIp} | Policy deployed: ${string(deployPolicy)}'
