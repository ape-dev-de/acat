// Azure Communication Services resource for acat (Teams-user calling).
// Deploy: az deployment group create -g <rg> -f infra/acs.bicep -p name=<acs-name>
@description('Name of the Communication Services resource (globally unique).')
param name string

@description('Data residency of the resource.')
@allowed(['Europe', 'Germany', 'United States', 'UK', 'Switzerland'])
param dataLocation string = 'Europe'

resource acs 'Microsoft.Communication/communicationServices@2023-04-01' = {
  name: name
  location: 'global'
  properties: {
    dataLocation: dataLocation
  }
}

output endpoint string = 'https://${acs.properties.hostName}/'
// The connection string (with access key) is a secret: read it with
//   az communication list-key -n <name> -g <rg> --query primaryConnectionString -o tsv
// and hand it to the relay as ACAT_ACS_CONNECTION_STRING. Never commit it.
