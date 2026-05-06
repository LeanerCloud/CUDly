output "role_definition_id" {
  description = "Resource ID of the Archera custom RBAC role definition (empty string when enable_archera = false)."
  value       = length(azurerm_role_definition.archera_integration) > 0 ? azurerm_role_definition.archera_integration[0].role_definition_resource_id : ""
}

output "role_assignment_id" {
  description = "Resource ID of the Archera role assignment (empty string when enable_archera = false)."
  value       = length(azurerm_role_assignment.archera_integration) > 0 ? azurerm_role_assignment.archera_integration[0].id : ""
}
