// ACI Datadog Agent module
// Always deployed — DD Agent is mandatory for Remote Configuration.

@description('Azure region')
param location string

@description('Resource name prefix')
param namePrefix string

@description('Datadog API key')
@secure()
param datadogApiKey string

@description('Datadog site (e.g. datadoghq.com, datadoghq.eu)')
param datadogSite string

@description('Resource ID of the ACI subnet')
param aciSubnetId string

@description('Enable CanNotDelete lock')
param enableLocks bool = true

@description('Custom tags to merge with default tags')
param customTags object = {}

var tags = union({ 'dd-component': 'apim-callout' }, customTags)

resource agentContainerGroup 'Microsoft.ContainerInstance/containerGroups@2023-05-01' = {
  name: '${namePrefix}-agent'
  location: location
  tags: tags
  properties: {
    osType: 'Linux'
    restartPolicy: 'Always'
    ipAddress: {
      type: 'Private'
      ports: [
        {
          port: 8126
          protocol: 'TCP'
        }
      ]
    }
    subnetIds: [
      {
        id: aciSubnetId
      }
    ]
    containers: [
      {
        name: 'datadog-agent'
        properties: {
          image: 'datadog/agent:latest'
          resources: {
            requests: {
              cpu: 1
              memoryInGB: 2
            }
          }
          ports: [
            {
              port: 8126
              protocol: 'TCP'
            }
          ]
          environmentVariables: [
            {
              name: 'DD_API_KEY'
              secureValue: datadogApiKey
            }
            {
              name: 'DD_SITE'
              value: datadogSite
            }
            {
              name: 'DD_APM_ENABLED'
              value: 'true'
            }
            {
              name: 'DD_APM_NON_LOCAL_TRAFFIC'
              value: 'true'
            }
            {
              name: 'DD_REMOTE_CONFIGURATION_ENABLED'
              value: 'true'
            }
            {
              name: 'DD_LOGS_ENABLED'
              value: 'false'
            }
            {
              name: 'DD_PROCESS_AGENT_ENABLED'
              value: 'false'
            }
            {
              name: 'DD_SYSTEM_PROBE_ENABLED'
              value: 'false'
            }
            {
              name: 'DD_APM_RECEIVER_PORT'
              value: '8126'
            }
            {
              name: 'DD_HOSTNAME'
              value: '${namePrefix}-agent-apim-callout'
            }
          ]
        }
      }
    ]
  }
}

resource lock 'Microsoft.Authorization/locks@2020-05-01' = if (enableLocks) {
  name: '${namePrefix}-agent-lock'
  scope: agentContainerGroup
  properties: {
    level: 'CanNotDelete'
    notes: 'Prevents accidental deletion of the Datadog Agent container group'
  }
}

output agentIp string = agentContainerGroup.properties.ipAddress.ip
