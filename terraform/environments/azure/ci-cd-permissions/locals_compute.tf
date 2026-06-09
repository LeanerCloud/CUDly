locals {
  compute_actions = [
    "Microsoft.App/containerApps/*",
    "Microsoft.App/managedEnvironments/*",
    "Microsoft.App/locations/*",
    "Microsoft.ContainerRegistry/registries/*",
    "Microsoft.ContainerRegistry/registries/tasks/*",
    "Microsoft.ContainerService/managedClusters/*",
    "Microsoft.Web/serverFarms/*",
    "Microsoft.Web/sites/*",
    "Microsoft.Logic/workflows/*",
    "Microsoft.Logic/locations/*",
    "Microsoft.Cdn/profiles/*",
    "Microsoft.Cdn/operationresults/*",
    "Microsoft.Cdn/checkNameAvailability/*",
    "Microsoft.ManagedIdentity/userAssignedIdentities/*",
    "Microsoft.ManagedIdentity/operations/*",
  ]
}
