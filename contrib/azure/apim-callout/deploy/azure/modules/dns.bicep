// Private DNS module: creates a DNS zone for the internal ACA environment
// so that APIM and other VNet resources can resolve the callout FQDN.

@description('Resource name prefix')
param namePrefix string

@description('Default domain of the ACA managed environment')
param acaDomain string

@description('Static IP of the ACA managed environment')
param acaStaticIp string

@description('Resource ID of the VNet to link the DNS zone to')
param vnetId string

@description('Custom tags to merge with default tags')
param customTags object = {}

var tags = union({ 'dd-component': 'apim-callout' }, customTags)

resource privateDnsZone 'Microsoft.Network/privateDnsZones@2020-06-01' = {
  name: acaDomain
  location: 'global'
  tags: tags
}

resource wildcardRecord 'Microsoft.Network/privateDnsZones/A@2020-06-01' = {
  parent: privateDnsZone
  name: '*'
  properties: {
    ttl: 300
    aRecords: [
      {
        ipv4Address: acaStaticIp
      }
    ]
  }
}

resource vnetLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = {
  parent: privateDnsZone
  name: '${namePrefix}-vnet-link'
  location: 'global'
  tags: tags
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}
