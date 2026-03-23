output "role_definition_id" {
  description = "ID of the CUDly Terraform deploy custom role"
  value       = azurerm_role_definition.cudly_deploy.id
}

output "role_assignment_id" {
  description = "ID of the role assignment"
  value       = azurerm_role_assignment.cudly_deploy.id
}
