// APIM Policy module: conditionally injects Datadog callout policy.
// Always invoked so outputs (fragments, patch command) are always available.
// The ARM policy resource is only created when deployPolicy=true.

@description('Whether to deploy the policy to APIM APIs')
param deployPolicy bool = false

@description('Name of the existing APIM service')
param apimServiceName string

@description('Array of API IDs to apply the policy to (empty = apply to All APIs at service level)')
param targetApiIds array = []

@description('Base URL of the callout service (from container-app module)')
param calloutBaseUrl string

@description('Raw policy XML content (loaded via loadTextContent in main.bicep)')
param policyXml string

// Substitute the placeholder with the actual callout URL
var processedPolicyXml = replace(policyXml, 'https://<dd-apim-callout-host>:8080', calloutBaseUrl)
// Global (service-level) policies do not allow <base /> — strip it for All APIs deployments
var globalPolicyXml = replace(processedPolicyXml, '<base />', '')
var applyToAllApis = empty(targetApiIds)

// Fragments for manual merge (use replace() since raw strings don't interpolate)
var inboundFragment = replace(
  '<!-- Datadog APIM Callout: Add inside <inbound> after <base /> -->\n<send-request mode="new" response-variable-name="ddPhase1Response" timeout="3" ignore-error="true">\n  <set-url>CALLOUT_URL/</set-url>\n  <!-- See azure-apim-full.xml for complete policy -->\n</send-request>',
  'CALLOUT_URL',
  calloutBaseUrl
)

var outboundFragment = replace(
  '<!-- Datadog APIM Callout: Add inside <outbound> after <base /> -->\n<send-request mode="new" response-variable-name="ddPhase3Response" timeout="3" ignore-error="true">\n  <set-url>CALLOUT_URL/</set-url>\n  <!-- See azure-apim-full.xml for complete policy -->\n</send-request>',
  'CALLOUT_URL',
  calloutBaseUrl
)

// Deploy policy to All APIs at service level (default)
resource globalPolicy 'Microsoft.ApiManagement/service/policies@2023-09-01-preview' = if (deployPolicy && applyToAllApis) {
  name: '${apimServiceName}/policy'
  properties: {
    format: 'rawxml'
    value: globalPolicyXml
  }
}

// Deploy policy to specific APIs (when targetApiIds is provided)
resource apiPolicy 'Microsoft.ApiManagement/service/apis/policies@2023-09-01-preview' = [
  for apiId in targetApiIds: if (deployPolicy) {
    name: '${apimServiceName}/${apiId}/policy'
    properties: {
      format: 'rawxml'
      value: processedPolicyXml
    }
  }
]

// --- Outputs (always available regardless of deployPolicy) ---
output calloutBaseUrl string = calloutBaseUrl
output policyPatchCommand string = 'sed -i \'s|https://<dd-apim-callout-host>:8080|${calloutBaseUrl}|g\' your-policy.xml'
output policyFragments object = {
  inbound: inboundFragment
  outbound: outboundFragment
}
output appliedApiIds array = deployPolicy ? (applyToAllApis ? ['*'] : targetApiIds) : []
