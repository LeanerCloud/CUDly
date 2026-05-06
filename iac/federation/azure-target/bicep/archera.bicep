// ==============================================
// Archera Integration — Azure (Federation Bundle, azure-target)
// ==============================================
//
// Alternative deployment path for customers who prefer Bicep over Terraform.
// The permission lists MUST stay in sync with iac/modules/archera/scope.azure.yaml
// — the CI gate scripts/check-archera-parity.sh enforces parity automatically.
//
// PROVISIONAL SCOPE — must be confirmed against Archera integration docs
// before setting enableArchera = true in any parameters file.
// TODO(@cristim): confirm Archera scope list against integration docs
// before enabling.  Reference: https://archera.ai/docs (integration guide).

targetScope = 'subscription'

@description('When true, provision the Archera custom RBAC role and role assignment. Leave false unless enrolled with Archera.')
param enableArchera bool = false

@description('Object ID of the service principal Archera provides during onboarding. NOT the Application/Client ID.')
param archeraAzureSpObjectId string = ''

@description('When true (and enableArchera = true), include reservation write actions in the Archera custom role.')
param enableArcheraPurchaseActions bool = false

// ── Permission lists (mirrors scope.azure.yaml) ────────────────────────────
var readActions = [
  'Microsoft.CostManagement/*/read'
  'Microsoft.Consumption/*/read'
  'Microsoft.Billing/*/read'
  'Microsoft.Capacity/reservations/read'
  'Microsoft.Capacity/reservationOrders/read'
]

var purchaseActions = [
  'Microsoft.Capacity/reservationOrders/write'
]

var allActions = enableArcheraPurchaseActions ? concat(readActions, purchaseActions) : readActions

// ── Custom RBAC role ────────────────────────────────────────────────────────
var roleDefName = guid(subscription().id, 'cudly-archera-integration')

resource archeraRoleDef 'Microsoft.Authorization/roleDefinitions@2022-04-01' = if (enableArchera) {
  name: roleDefName
  properties: {
    roleName: 'CUDly Archera Integration'
    description: 'Archera integration role — read cost data, optionally purchase RIs. Provisional — confirm scope before enabling.'
    type: 'CustomRole'
    permissions: [
      {
        actions: allActions
        notActions: []
        dataActions: []
        notDataActions: []
      }
    ]
    assignableScopes: [
      subscription().id
    ]
  }
}

// ── Role assignment ─────────────────────────────────────────────────────────
resource archeraRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableArchera) {
  name: guid(subscription().id, archeraAzureSpObjectId, roleDefName)
  properties: {
    roleDefinitionId: archeraRoleDef.id
    principalId: archeraAzureSpObjectId
    principalType: 'ServicePrincipal'
  }
}

// ── Outputs ─────────────────────────────────────────────────────────────────
output roleDefinitionId string = enableArchera ? archeraRoleDef.id : ''
output roleAssignmentId string = enableArchera ? archeraRoleAssignment.id : ''
