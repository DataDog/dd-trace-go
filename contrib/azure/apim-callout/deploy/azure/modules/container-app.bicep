// Container App module: ACA environment + callout service
// Always deployed — the callout service is the core workload.

@description('Azure region')
param location string

@description('Resource name prefix')
param namePrefix string

@description('Container image for the callout service')
param containerImage string

@description('DD Agent host IP (from agent module output)')
param agentHost string

@description('Minimum number of replicas')
param minReplicas int

@description('Maximum number of replicas')
param maxReplicas int

@description('KEDA HTTP scaler concurrent requests threshold')
param concurrentRequestsThreshold string

@description('Resource ID of the ACA subnet')
param acaSubnetId string

@description('Resource ID of the Log Analytics workspace (optional)')
param logAnalyticsWorkspaceId string = ''

@description('Enforce HTTPS on the callout ingress (ACA terminates TLS automatically)')
param enableHttps bool = false

@description('Enable CanNotDelete lock')
param enableLocks bool = true

@description('Custom tags to merge with default tags')
param customTags object = {}

var tags = union({ 'dd-component': 'apim-callout' }, customTags)

var hasLogAnalytics = !empty(logAnalyticsWorkspaceId)

// Parse Log Analytics workspace details (only when provided)
var logAnalyticsWorkspaceName = hasLogAnalytics ? last(split(logAnalyticsWorkspaceId, '/')) : ''
var logAnalyticsResourceGroup = hasLogAnalytics ? split(logAnalyticsWorkspaceId, '/')[4] : ''

resource logAnalyticsWorkspace 'Microsoft.OperationalInsights/workspaces@2022-10-01' existing = if (hasLogAnalytics) {
  name: logAnalyticsWorkspaceName
  scope: resourceGroup(logAnalyticsResourceGroup)
}

// --- ACA Managed Environment ---
resource acaEnvironment 'Microsoft.App/managedEnvironments@2024-03-01' = {
  name: '${namePrefix}-env'
  location: location
  tags: tags
  properties: {
    vnetConfiguration: {
      infrastructureSubnetId: acaSubnetId
      internal: true
    }
    workloadProfiles: [
      {
        name: 'Consumption'
        workloadProfileType: 'Consumption'
      }
    ]
    appLogsConfiguration: hasLogAnalytics ? {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: logAnalyticsWorkspace!.properties.customerId
        sharedKey: logAnalyticsWorkspace!.listKeys().primarySharedKey
      }
    } : {
      destination: ''
    }
  }
}

// --- Container App ---
resource containerApp 'Microsoft.App/containerApps@2024-03-01' = {
  name: '${namePrefix}-callout'
  location: location
  tags: tags
  properties: {
    managedEnvironmentId: acaEnvironment.id
    workloadProfileName: 'Consumption'
    configuration: {
      ingress: {
        external: true // Combined with internal:true on the environment = VNet-accessible only
        targetPort: 8080
        transport: 'http'
        allowInsecure: !enableHttps
      }
    }
    template: {
      containers: [
        {
          name: 'apim-callout'
          image: containerImage
          resources: {
            cpu: json('0.5')
            memory: '1Gi'
          }
          env: [
            {
              name: 'DD_AGENT_HOST'
              value: agentHost
            }
            {
              name: 'DD_APPSEC_ENABLED'
              value: 'true'
            }
          ]
          probes: [
            {
              type: 'Liveness'
              httpGet: {
                port: 8081
                path: '/'
              }
              initialDelaySeconds: 5
              periodSeconds: 10
            }
            {
              type: 'Readiness'
              httpGet: {
                port: 8081
                path: '/'
              }
              initialDelaySeconds: 3
              periodSeconds: 5
            }
          ]
        }
      ]
      scale: {
        minReplicas: minReplicas
        maxReplicas: maxReplicas
        rules: [
          {
            name: 'http-scaler'
            http: {
              metadata: {
                concurrentRequests: concurrentRequestsThreshold
              }
            }
          }
        ]
      }
    }
  }
}

// --- Diagnostic Settings (only when Log Analytics is provided) ---
// The GA API version (2021-05-01) does not exist for diagnosticSettings; the preview version is required.
resource diagnostics 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (hasLogAnalytics) {
  name: '${namePrefix}-diagnostics'
  scope: acaEnvironment
  properties: {
    workspaceId: logAnalyticsWorkspaceId
    logs: [
      {
        category: 'ContainerAppConsoleLogs'
        enabled: true
      }
      {
        category: 'ContainerAppSystemLogs'
        enabled: true
      }
    ]
  }
}

resource lock 'Microsoft.Authorization/locks@2020-05-01' = if (enableLocks) {
  name: '${namePrefix}-env-lock'
  scope: acaEnvironment
  properties: {
    level: 'CanNotDelete'
    notes: 'Prevents accidental deletion of the APIM callout ACA environment'
  }
}

output acaFqdn string = containerApp.properties.configuration.ingress.fqdn
output calloutBaseUrl string = '${enableHttps ? 'https' : 'http'}://${containerApp.properties.configuration.ingress.fqdn}'
output acaDefaultDomain string = acaEnvironment.properties.defaultDomain
output acaStaticIp string = acaEnvironment.properties.staticIp
