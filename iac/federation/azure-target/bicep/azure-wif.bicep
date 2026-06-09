// CUDly Azure subscription role assignment for Workload Identity Federation.
//
// This template assigns the Reservation Purchaser built-in role to an existing
// service principal at the subscription scope. Identity setup (App Registration,
// service principal, certificate upload) is NOT performed here — that step
// requires Microsoft Graph API calls that are only available via the preview
// `microsoftGraphV1` Bicep extension. Run the CUDly azure-wif-cli.sh script
// first to create the identity, then deploy this template with the resulting
// service principal object ID.
//
// Two-step flow:
//   1. bash target-azure-wif-cli.sh   (creates app registration + service principal)
//   2. az deployment sub create --template-file azure-wif.bicep \
//        --parameters @target-azure-wif-bicep-params.json \
//        --parameters servicePrincipalObjectId=<OBJECT_ID_FROM_STEP_1> \
//        --location <region>

targetScope = 'subscription'

@description('Object ID of the CUDly service principal created by the azure-wif-cli.sh script.')
param servicePrincipalObjectId string

@description('Built-in role definition ID for Reservation Purchaser. Default is the well-known built-in role ID.')
param roleDefinitionId string = 'f7b75c60-3036-4b75-91c3-6b41c27c1689'

@description('Deterministic GUID for the role assignment. Leave the default unless you need to avoid a collision.')
param roleAssignmentName string = guid(subscription().id, servicePrincipalObjectId, roleDefinitionId)

resource reservationPurchaser 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: roleAssignmentName
  properties: {
    principalId: servicePrincipalObjectId
    principalType: 'ServicePrincipal'
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)
    description: 'CUDly Reservation Purchaser — assigned via CUDly federation setup.'
  }
}

output roleAssignmentId string = reservationPurchaser.id
output principalId string = servicePrincipalObjectId
