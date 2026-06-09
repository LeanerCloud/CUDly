locals {
  data_actions = [
    "Microsoft.DBforPostgreSQL/flexibleServers/*",
    "Microsoft.KeyVault/vaults/*",
    "Microsoft.KeyVault/operations/*",
    "Microsoft.KeyVault/checkNameAvailability/*",
    "Microsoft.Storage/storageAccounts/*",
    "Microsoft.Storage/storageAccounts/blobServices/*",
    "Microsoft.Storage/storageAccounts/blobServices/containers/*",
    "Microsoft.Communication/CommunicationServices/*",
    "Microsoft.Communication/EmailServices/*",
    "Microsoft.Authorization/roleAssignments/read",
    "Microsoft.Authorization/roleAssignments/write",
    "Microsoft.Authorization/roleAssignments/delete",
    "Microsoft.Authorization/roleDefinitions/read",
  ]
}
