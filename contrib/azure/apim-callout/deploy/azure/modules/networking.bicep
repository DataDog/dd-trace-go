// Networking module: VNet, 3 subnets, NSG, NAT gateway
// Always invoked — VNet is mandatory for DD Agent and ACA.
//
// When existingVnetId is provided, existingAcaSubnetId and existingAciSubnetId
// must also be provided. No new networking resources are created in that case.

@description('Azure region for all networking resources')
param location string

@description('Resource name prefix')
param namePrefix string

@description('VNet address space')
param vnetAddressPrefix string = '10.0.0.0/16'

@description('ACA subnet CIDR (must be /27 or larger, within VNet address space)')
param acaSubnetPrefix string = '10.0.1.0/27'

@description('ACI subnet CIDR (must be /24 or larger, within VNet address space)')
param aciSubnetPrefix string = '10.0.2.0/24'

@description('APIM integration subnet CIDR (/27, within VNet address space)')
param apimSubnetPrefix string = '10.0.3.0/27'

@description('Resource ID of an existing VNet (skip all networking creation if provided)')
param existingVnetId string = ''

@description('Resource ID of an existing ACA subnet (required when existingVnetId is set)')
param existingAcaSubnetId string = ''

@description('Resource ID of an existing ACI subnet (required when existingVnetId is set)')
param existingAciSubnetId string = ''

@description('Custom tags to merge with default tags')
param customTags object = {}

// When an existing VNet is provided, skip all resource creation.
var createNetworking = empty(existingVnetId)

var tags = union({ 'dd-component': 'apim-callout' }, customTags)

// --- NSG (only when creating networking) ---
resource nsg 'Microsoft.Network/networkSecurityGroups@2023-11-01' = if (createNetworking) {
  name: '${namePrefix}-nsg'
  location: location
  tags: tags
  properties: {
    securityRules: [
      {
        name: 'AllowAcaToAciApm'
        properties: {
          priority: 100
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: acaSubnetPrefix
          destinationAddressPrefix: aciSubnetPrefix
          sourcePortRange: '*'
          destinationPortRange: '8126'
        }
      }
      {
        name: 'DenyInternetInboundToAci'
        properties: {
          priority: 4000
          direction: 'Inbound'
          access: 'Deny'
          protocol: '*'
          sourceAddressPrefix: 'Internet'
          destinationAddressPrefix: aciSubnetPrefix
          sourcePortRange: '*'
          destinationPortRange: '*'
        }
      }
    ]
  }
}

// --- NAT Gateway (only when creating networking) ---
resource natPublicIp 'Microsoft.Network/publicIPAddresses@2023-11-01' = if (createNetworking) {
  name: '${namePrefix}-nat-pip'
  location: location
  tags: tags
  sku: {
    name: 'Standard'
  }
  properties: {
    publicIPAllocationMethod: 'Static'
  }
}

resource natGateway 'Microsoft.Network/natGateways@2023-11-01' = if (createNetworking) {
  name: '${namePrefix}-nat'
  location: location
  tags: tags
  sku: {
    name: 'Standard'
  }
  properties: {
    publicIpAddresses: [
      {
        id: natPublicIp.id
      }
    ]
    idleTimeoutInMinutes: 10
  }
}

// --- APIM NSG (required for APIM VNet integration) ---
resource apimNsg 'Microsoft.Network/networkSecurityGroups@2023-11-01' = if (createNetworking) {
  name: '${namePrefix}-apim-nsg'
  location: location
  tags: tags
  properties: {
    securityRules: [
      {
        name: 'AllowApimManagement'
        properties: {
          priority: 100
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'ApiManagement'
          destinationAddressPrefix: 'VirtualNetwork'
          sourcePortRange: '*'
          destinationPortRange: '3443'
        }
      }
      {
        name: 'AllowClientToGateway'
        properties: {
          priority: 110
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'Internet'
          destinationAddressPrefix: 'VirtualNetwork'
          sourcePortRange: '*'
          destinationPortRange: '443'
        }
      }
      {
        name: 'AllowLoadBalancer'
        properties: {
          priority: 120
          direction: 'Inbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'AzureLoadBalancer'
          destinationAddressPrefix: 'VirtualNetwork'
          sourcePortRange: '*'
          destinationPortRange: '6390'
        }
      }
      {
        name: 'AllowStorageOutbound'
        properties: {
          priority: 100
          direction: 'Outbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'VirtualNetwork'
          destinationAddressPrefix: 'Storage'
          sourcePortRange: '*'
          destinationPortRange: '443'
        }
      }
      {
        name: 'AllowSqlOutbound'
        properties: {
          priority: 110
          direction: 'Outbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'VirtualNetwork'
          destinationAddressPrefix: 'Sql'
          sourcePortRange: '*'
          destinationPortRange: '1433'
        }
      }
      {
        name: 'AllowAzureAdOutbound'
        properties: {
          priority: 120
          direction: 'Outbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'VirtualNetwork'
          destinationAddressPrefix: 'AzureActiveDirectory'
          sourcePortRange: '*'
          destinationPortRange: '443'
        }
      }
      {
        name: 'AllowKeyVaultOutbound'
        properties: {
          priority: 130
          direction: 'Outbound'
          access: 'Allow'
          protocol: 'Tcp'
          sourceAddressPrefix: 'VirtualNetwork'
          destinationAddressPrefix: 'AzureKeyVault'
          sourcePortRange: '*'
          destinationPortRange: '443'
        }
      }
    ]
  }
}

// --- VNet (only when creating networking) ---
resource vnet 'Microsoft.Network/virtualNetworks@2023-11-01' = if (createNetworking) {
  name: '${namePrefix}-vnet'
  location: location
  tags: tags
  properties: {
    addressSpace: {
      addressPrefixes: [
        vnetAddressPrefix
      ]
    }
    subnets: [
      {
        name: 'aca-subnet'
        properties: {
          addressPrefix: acaSubnetPrefix
          delegations: [
            {
              name: 'Microsoft.App.environments'
              properties: {
                serviceName: 'Microsoft.App/environments'
              }
            }
          ]
        }
      }
      {
        name: 'aci-subnet'
        properties: {
          addressPrefix: aciSubnetPrefix
          networkSecurityGroup: {
            id: nsg.id
          }
          natGateway: {
            id: natGateway.id
          }
          delegations: [
            {
              name: 'Microsoft.ContainerInstance.containerGroups'
              properties: {
                serviceName: 'Microsoft.ContainerInstance/containerGroups'
              }
            }
          ]
        }
      }
      {
        name: 'apim-subnet'
        properties: {
          addressPrefix: apimSubnetPrefix
          networkSecurityGroup: {
            id: apimNsg.id
          }
        }
      }
    ]
  }
}

// --- Outputs ---
output acaSubnetId string = createNetworking ? '${vnet.id}/subnets/aca-subnet' : existingAcaSubnetId
output aciSubnetId string = createNetworking ? '${vnet.id}/subnets/aci-subnet' : existingAciSubnetId
output apimSubnetId string = createNetworking ? '${vnet.id}/subnets/apim-subnet' : ''
output vnetId string = createNetworking ? vnet.id : existingVnetId
