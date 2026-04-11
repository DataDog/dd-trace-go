using '../main.bicep'

param minReplicas = 1
param maxReplicas = 10
param concurrentRequestsThreshold = '20'
// deployPolicy intentionally omitted — deploy.sh computes smart default
// datadogApiKey, apimServiceName, targetApiIds, logAnalyticsWorkspaceId must be provided at deploy time
